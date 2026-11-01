// Package mailer is the platform's one email sender: a tenant's own email_smtp
// connection (workflow send_email, the agent's send_email tool) and the
// platform relay (password resets, invitations, approvals, alerts) both go
// through Send.
//
// It began in tooling-lib, where it merged the workflow node's and the agent
// tool's two copies (ADR 059 §4). It moved here (ADR 090) because system mail
// lives in core-lib, which cannot import tooling-lib, and a second sender for
// system mail meant no single place to record that a mail left.
//
// Which files a message carries is each engine's own vocabulary, so each engine
// resolves its own files and hands over []Attachment. Everything downstream of
// that — the ceiling, the MIME, the TLS mode, the transport — is here, once.
package mailer

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"

	"github.com/wneessen/go-mail"
)

// MaxAttachmentBytes bounds one message's attachments.
//
// Checked here rather than left to the server, so the failure names the total
// and the limit instead of arriving as a 552 from a remote mid-send, after the
// connection is open and part of the message has gone.
const MaxAttachmentBytes = 20 << 20

// Account is the connection's half of the arrangement: where to send, as whom,
// and with what login.
//
// The fields are the email_smtp connection's, because that is the row both
// engines already spend. ImplicitTLS is the one translation: the connection
// spells the transport as a bool, and the two transports are implicit TLS on
// 465 or STARTTLS everywhere else. There is no third option — plaintext is not
// offered, and cert verification is never disabled.
type Account struct {
	Host        string
	Port        int
	Username    string
	Password    string
	From        string
	FromName    string
	ImplicitTLS bool
	// HELODomain sets the EHLO name when the relay insists on a specific one.
	// Empty lets go-mail use the local hostname.
	HELODomain string
}

// AccountFromCreds reads a resolved email_smtp connection.
//
// Both engines resolved this map field-for-field in their own copy; the key
// names are the connection type's, not either engine's.
func AccountFromCreds(creds map[string]any) (Account, error) {
	a := Account{
		Host:        CredString(creds, "host"),
		Port:        CredInt(creds, "port", 587),
		Username:    CredString(creds, "username"),
		Password:    CredString(creds, "password"),
		From:        CredString(creds, "from"),
		FromName:    CredString(creds, "from_name"),
		ImplicitTLS: CredBool(creds, "tls"),
		HELODomain:  CredString(creds, "helo_domain"),
	}
	if a.Host == "" {
		return a, fmt.Errorf("this connection has no SMTP server set")
	}
	if a.From == "" {
		return a, fmt.Errorf("this connection has no from address set")
	}
	return a, nil
}

// Attachment is one file to travel with the message, already resolved by
// whichever engine is sending.
//
// Open is a function rather than an io.Reader so that nothing is read — and no
// stream is left dangling — when the message turns out to be malformed, or when
// an earlier attachment has already pushed the total over the ceiling.
type Attachment struct {
	Name string
	Size int64
	Open func(context.Context) (io.ReadCloser, error)
	// Inline EMBEDS the file in the body instead of hanging it off the message,
	// so an HTML body can show it with <img src="cid:NAME"> where NAME is this
	// Name. The Content-ID is the name, which is why the two must agree: a body
	// referencing a cid nothing embeds renders a broken image, and that is the
	// only way this can fail — silently, at the reader.
	//
	// One flag on one list rather than a second list on Message, so the ceiling
	// below counts embedded and attached files in a single sweep and neither
	// engine grows a parallel path for the same bytes.
	Inline bool
}

// Message is what to send. Addresses are already split: each engine's parameter
// takes a different shape (a comma string, a JSON array) and AddressList is here
// for the ones that take the comma form.
type Message struct {
	// Kind names what the message is ("core.password_reset",
	// "workflow.send_email"). Required: it is what the delivery log shows in
	// place of the subject.
	Kind        string
	Origin      Origin
	To          []string
	Cc          []string
	Bcc         []string
	ReplyTo     string
	Subject     string
	Body        string
	HTML        bool
	Attachments []Attachment
	// Headers are written verbatim, after the ones the message builds itself.
	// A name or value carrying CR or LF is refused: it would let the value
	// write headers of its own.
	Headers map[string]string
}

// Origin is the run a message was sent from, when there is one.
type Origin struct {
	Type string // OriginWorkflowRun | OriginAgentRun
	ID   string
}

const (
	OriginWorkflowRun = "workflow_run"
	OriginAgentRun    = "agent_run"
)

// SizeError is the attachment ceiling, reported rather than phrased: the engines
// address different readers ("this step" vs a model being told why its tool
// call failed), so each writes its own sentence around these two numbers.
type SizeError struct{ Total, Max int64 }

func (e *SizeError) Error() string {
	return fmt.Sprintf("attachments total %d bytes, over the %d byte limit", e.Total, e.Max)
}

// AttachError is a file that could not be read back at send time — collected,
// or belonging to another tenant. Carries the name so the caller can say which.
type AttachError struct {
	Name string
	Err  error
}

func (e *AttachError) Error() string { return fmt.Sprintf("attaching %s: %v", e.Name, e.Err) }
func (e *AttachError) Unwrap() error { return e.Err }

// Compose builds the MIME message, attachments included.
//
// Everything that can fail without touching the network fails HERE, before
// Client is ever dialled: a bad address, a missing file, an oversized set.
// Sending is the side effect that cannot be taken back, and half a message
// going out before the attachment step notices is the worst of both.
func Compose(ctx context.Context, a Account, m Message) (*mail.Msg, error) {
	if strings.TrimSpace(m.Kind) == "" {
		return nil, fmt.Errorf("the message has no kind")
	}
	msg := mail.NewMsg()
	if a.FromName != "" {
		if err := msg.FromFormat(a.FromName, a.From); err != nil {
			return nil, fmt.Errorf("the from address %q is not valid", a.From)
		}
	} else if err := msg.From(a.From); err != nil {
		return nil, fmt.Errorf("the from address %q is not valid", a.From)
	}

	if len(m.To) == 0 {
		return nil, fmt.Errorf("there is no recipient — the To field resolved to nothing")
	}
	if err := msg.To(m.To...); err != nil {
		return nil, fmt.Errorf("a recipient is not a valid address: %w", err)
	}
	if len(m.Cc) > 0 {
		if err := msg.Cc(m.Cc...); err != nil {
			return nil, fmt.Errorf("a Cc recipient is not a valid address: %w", err)
		}
	}
	if len(m.Bcc) > 0 {
		if err := msg.Bcc(m.Bcc...); err != nil {
			return nil, fmt.Errorf("a Bcc recipient is not a valid address: %w", err)
		}
	}
	if m.ReplyTo != "" {
		if err := msg.ReplyTo(m.ReplyTo); err != nil {
			return nil, fmt.Errorf("the reply-to address %q is not valid", m.ReplyTo)
		}
	}

	for name, value := range m.Headers {
		if err := validHeader(name, value); err != nil {
			return nil, err
		}
		msg.SetGenHeader(mail.Header(name), value)
	}

	msg.Subject(m.Subject)
	if m.HTML {
		msg.SetBodyString(mail.TypeTextHTML, m.Body)
		// A client that cannot render HTML would otherwise show nothing.
		msg.AddAlternativeString(mail.TypeTextPlain, StripTags(m.Body))
	} else {
		msg.SetBodyString(mail.TypeTextPlain, m.Body)
	}

	if err := attach(ctx, msg, m.Attachments); err != nil {
		return nil, err
	}
	return msg, nil
}

// validHeader refuses a field name outside RFC 5322's printable set or a value
// with a line break in it.
func validHeader(name, value string) error {
	if name == "" {
		return fmt.Errorf("a header has no name")
	}
	for _, r := range name {
		if r <= ' ' || r > '~' || r == ':' {
			return fmt.Errorf("the header name %q is not valid", name)
		}
	}
	if strings.ContainsAny(value, "\r\n") {
		return fmt.Errorf("the header %s carries a line break", name)
	}
	return nil
}

// attach streams each file into a MIME part, refusing the whole set the moment
// it goes over the ceiling.
//
// The sizes are the ones the caller already knows from the store's metadata, so
// the refusal happens before a single byte is read.
func attach(ctx context.Context, msg *mail.Msg, as []Attachment) error {
	var total int64
	for _, at := range as {
		total += at.Size
		if total > MaxAttachmentBytes {
			return &SizeError{Total: total, Max: MaxAttachmentBytes}
		}
	}
	for _, at := range as {
		rc, err := at.Open(ctx)
		if err != nil {
			return &AttachError{Name: at.Name, Err: err}
		}
		// Both readers consume the stream; go-mail buffers it to build the MIME
		// part, which is why the size ceiling above is the real guard.
		if at.Inline {
			// go-mail writes the embed's own Content-ID as <name>, which is
			// what an <img src="cid:name"> in the body resolves against — the
			// header carries the angle brackets, the cid: URL does not
			// (RFC 2392). Nothing to pass: setting it by hand here would only
			// duplicate the default, and would bypass the filename sanitising
			// the default applies, so a name the writer cleans up would stop
			// matching the cid the body names.
			err = msg.EmbedReader(at.Name, rc)
		} else {
			err = msg.AttachReader(at.Name, rc)
		}
		_ = rc.Close()
		if err != nil {
			return &AttachError{Name: at.Name, Err: err}
		}
	}
	return nil
}

// Client dials as this account.
//
// TLS is mandatory in both branches. go-mail's own default is TLSMandatory, so
// a connection whose `tls` is false gets STARTTLS — not plaintext, which this
// platform does not offer at all.
func (a Account) Client() (*mail.Client, error) {
	opts := []mail.Option{mail.WithPort(a.Port)}
	if a.Username == "" {
		opts = append(opts, mail.WithSMTPAuth(mail.SMTPAuthNoAuth))
	} else {
		opts = append(opts,
			// Auto-discover rather than a fixed mechanism: providers disagree
			// about PLAIN versus LOGIN, and picking one turns a working
			// credential into an authentication failure on half of them.
			mail.WithSMTPAuth(mail.SMTPAuthAutoDiscover),
			mail.WithUsername(a.Username),
			mail.WithPassword(a.Password),
		)
	}
	if a.ImplicitTLS {
		opts = append(opts, mail.WithSSL())
	} else {
		opts = append(opts, mail.WithTLSPolicy(mail.TLSMandatory))
	}
	if a.HELODomain != "" {
		opts = append(opts, mail.WithHELO(a.HELODomain))
	}
	return mail.NewClient(a.Host, opts...)
}

// Send composes and delivers in one call, which is what both callers want.
// Compose separately only when the message itself is under test.
func Send(ctx context.Context, a Account, m Message) error {
	msg, err := Compose(ctx, a, m)
	if err != nil {
		return err
	}
	client, err := a.Client()
	if err != nil {
		return fmt.Errorf("reach %s: %w", a.Host, err)
	}
	if err := client.DialAndSendWithContext(ctx, msg); err != nil {
		return fmt.Errorf("send through %s: %w", a.Host, err)
	}
	return nil
}

// AddressList splits the comma-separated form a single-field parameter takes.
// Empty entries are dropped rather than rejected: "{{ manager }}, {{ deputy }}"
// with no deputy is an ordinary shape, not a mistake.
func AddressList(s string) []string {
	var out []string
	for _, part := range strings.Split(s, ",") {
		if v := strings.TrimSpace(part); v != "" {
			out = append(out, v)
		}
	}
	return out
}

// StripTags produces the plain-text alternative. It is deliberately crude — it
// is a fallback for a client that cannot render HTML at all, not a renderer.
func StripTags(s string) string {
	var b strings.Builder
	depth := 0
	for _, r := range s {
		switch {
		case r == '<':
			depth++
		case r == '>' && depth > 0:
			depth--
		case depth == 0:
			b.WriteRune(r)
		}
	}
	return strings.TrimSpace(b.String())
}

func CredString(m map[string]any, key string) string {
	if v, ok := m[key].(string); ok {
		return strings.TrimSpace(v)
	}
	return ""
}

// CredInt reads a port. JSON numbers arrive as float64, and a form that stored
// one as text is common enough to be worth accepting rather than failing on.
func CredInt(m map[string]any, key string, fallback int) int {
	switch v := m[key].(type) {
	case float64:
		if v > 0 {
			return int(v)
		}
	case int:
		if v > 0 {
			return v
		}
	case json.Number:
		if n, err := v.Int64(); err == nil && n > 0 {
			return int(n)
		}
	case string:
		var n int
		if _, err := fmt.Sscanf(strings.TrimSpace(v), "%d", &n); err == nil && n > 0 {
			return n
		}
	}
	return fallback
}

func CredBool(m map[string]any, key string) bool {
	switch v := m[key].(type) {
	case bool:
		return v
	case string:
		return strings.EqualFold(strings.TrimSpace(v), "true")
	}
	return false
}
