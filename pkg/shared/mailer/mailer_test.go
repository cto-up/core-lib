package mailer_test

import (
	"bytes"
	"context"
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/cto-up/core-lib/pkg/shared/mailer"
)

func account() mailer.Account {
	return mailer.Account{Host: "smtp.example.com", Port: 587, Username: "u", Password: "p", From: "from@example.com"}
}

func attachment(name, body string) mailer.Attachment {
	return mailer.Attachment{
		Name: name,
		Size: int64(len(body)),
		Open: func(context.Context) (io.ReadCloser, error) {
			return io.NopCloser(strings.NewReader(body)), nil
		},
	}
}

func compose(t *testing.T, m mailer.Message) string {
	t.Helper()
	msg, err := mailer.Compose(context.Background(), account(), m)
	if err != nil {
		t.Fatalf("compose: %v", err)
	}
	var out bytes.Buffer
	if _, err := msg.WriteTo(&out); err != nil {
		t.Fatalf("write: %v", err)
	}
	return out.String()
}

// The whole point of the package: an employee's send_email could not do this at
// all, because its message was a hardcoded single text/plain part.
func TestAttachmentsBecomeMIMEParts(t *testing.T) {
	body := compose(t, mailer.Message{Kind: "test",
		To:      []string{"to@example.com"},
		Subject: "Invoices",
		Body:    "Attached.",
		Attachments: []mailer.Attachment{
			attachment("F-2026-01.pdf", "invoice bytes"),
			attachment("R-2026-01.pdf", "receipt bytes"),
		},
	})
	for _, want := range []string{"F-2026-01.pdf", "R-2026-01.pdf"} {
		if !strings.Contains(body, want) {
			t.Errorf("the composed message does not carry %q", want)
		}
	}
	if !strings.Contains(body, "multipart/mixed") {
		t.Error("a message with attachments must be multipart/mixed")
	}
}

// An embedded file is shown IN the body rather than hung off the message, which
// is what an HTML mail with a picture in it actually needs.
func TestInlineAttachmentsAreEmbeddedNotAttached(t *testing.T) {
	body := compose(t, mailer.Message{Kind: "test",
		To:      []string{"to@example.com"},
		Subject: "Look",
		Body:    `<p>Voici : <img src="cid:robot.png"></p>`,
		HTML:    true,
		Attachments: []mailer.Attachment{
			{Name: "robot.png", Size: 4, Inline: true,
				Open: func(context.Context) (io.ReadCloser, error) {
					return io.NopCloser(strings.NewReader("\x89PNG")), nil
				}},
		},
	})
	// Case-insensitively: Go's textproto canonicalises the key per hyphen
	// segment, so the wire spelling is "Content-Id", not "Content-ID".
	if !strings.Contains(strings.ToLower(body), "content-id: <robot.png>") {
		t.Errorf("an embedded file needs a Content-ID the body can reference:\n%s", body)
	}
	if !strings.Contains(body, "robot.png") {
		t.Error("the embedded file is not in the message")
	}
	if !strings.Contains(body, "inline") {
		t.Error("an embedded file should be disposed inline, not as an attachment")
	}
}

// The ceiling is about bytes, and an embedded picture is bytes.
func TestInlineFilesCountTowardTheSameCeiling(t *testing.T) {
	huge := mailer.Attachment{
		Name: "huge.png", Size: mailer.MaxAttachmentBytes + 1, Inline: true,
		Open: func(context.Context) (io.ReadCloser, error) {
			return io.NopCloser(strings.NewReader("x")), nil
		},
	}
	_, err := mailer.Compose(context.Background(), account(), mailer.Message{Kind: "test",
		To: []string{"to@example.com"}, Subject: "s", HTML: true,
		Attachments: []mailer.Attachment{huge},
	})
	var se *mailer.SizeError
	if !errors.As(err, &se) {
		t.Fatalf("got %v, want a *SizeError — embedding is not a way around the limit", err)
	}
}

// Mixed: one shown in the body, one hung off the message, in one send.
func TestInlineAndAttachedTravelTogether(t *testing.T) {
	body := compose(t, mailer.Message{Kind: "test",
		To: []string{"to@example.com"}, Subject: "Report", HTML: true,
		Body: `<p><img src="cid:chart.png"></p>`,
		Attachments: []mailer.Attachment{
			{Name: "chart.png", Size: 4, Inline: true,
				Open: func(context.Context) (io.ReadCloser, error) {
					return io.NopCloser(strings.NewReader("\x89PNG")), nil
				}},
			attachment("data.csv", "a,b\n1,2\n"),
		},
	})
	for _, want := range []string{"chart.png", "data.csv"} {
		if !strings.Contains(body, want) {
			t.Errorf("the message does not carry %q", want)
		}
	}
	if !strings.Contains(strings.ToLower(body), "content-id: <chart.png>") {
		t.Error("the embedded file lost its Content-ID when an attachment travelled with it")
	}
	if !strings.Contains(body, "multipart/related") {
		t.Error("an embed belongs in a related part, or the body cannot reference it")
	}
}

func TestPlainBodyIsASinglePart(t *testing.T) {
	body := compose(t, mailer.Message{Kind: "test", To: []string{"to@example.com"}, Subject: "Hi", Body: "Plain."})
	if !strings.Contains(body, "text/plain") {
		t.Error("want a text/plain body")
	}
	if strings.Contains(body, "text/html") {
		t.Error("a non-HTML message must not carry an HTML part")
	}
}

// HTML sends a plain-text alternative too, or a client that cannot render HTML
// shows nothing at all.
func TestHTMLCarriesAPlainAlternative(t *testing.T) {
	body := compose(t, mailer.Message{Kind: "test",
		To: []string{"to@example.com"}, Subject: "Hi", Body: "<p>Bonjour</p>", HTML: true,
	})
	if !strings.Contains(body, "text/html") || !strings.Contains(body, "text/plain") {
		t.Error("an HTML message must carry both parts")
	}
	if !strings.Contains(body, "Bonjour") {
		t.Error("the plain alternative should carry the stripped text")
	}
}

// Over the ceiling fails while nothing has been sent, and before a single byte
// is read: the alternative is a 552 from the remote after the connection is
// open and part of the message has gone.
func TestOversizedAttachmentsAreRefusedBeforeAnythingIsRead(t *testing.T) {
	read := false
	huge := mailer.Attachment{
		Name: "huge.csv",
		Size: mailer.MaxAttachmentBytes + 1,
		Open: func(context.Context) (io.ReadCloser, error) {
			read = true
			return io.NopCloser(strings.NewReader("x")), nil
		},
	}
	_, err := mailer.Compose(context.Background(), account(), mailer.Message{Kind: "test",
		To: []string{"to@example.com"}, Subject: "s", Attachments: []mailer.Attachment{huge},
	})
	var se *mailer.SizeError
	if !errors.As(err, &se) {
		t.Fatalf("got %v, want a *SizeError the caller can phrase itself", err)
	}
	if read {
		t.Error("the ceiling must be checked from the known sizes, before opening anything")
	}
}

// A file that has been collected must fail rather than send a message that was
// supposed to carry the invoice and silently did not.
func TestAnUnreadableAttachmentNamesTheFile(t *testing.T) {
	gone := mailer.Attachment{
		Name: "gone.csv",
		Size: 10,
		Open: func(context.Context) (io.ReadCloser, error) { return nil, io.ErrUnexpectedEOF },
	}
	_, err := mailer.Compose(context.Background(), account(), mailer.Message{Kind: "test",
		To: []string{"to@example.com"}, Subject: "s", Attachments: []mailer.Attachment{gone},
	})
	var ae *mailer.AttachError
	if !errors.As(err, &ae) || ae.Name != "gone.csv" {
		t.Fatalf("got %v, want an *AttachError naming gone.csv", err)
	}
}

func TestNoRecipientIsRefused(t *testing.T) {
	_, err := mailer.Compose(context.Background(), account(), mailer.Message{Kind: "test", Subject: "s"})
	if err == nil {
		t.Fatal("a message with no recipient must not compose")
	}
}

// The credential vocabulary is the email_smtp connection's, which is the row
// both engines already spend.
func TestAccountFromCredsReadsTheConnectionVocabulary(t *testing.T) {
	a, err := mailer.AccountFromCreds(map[string]any{
		"host": "smtp.ionos.fr", "port": float64(465), "username": "u",
		"password": "p", "from": "hello@x.fr", "from_name": "Hello", "tls": true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if a.Host != "smtp.ionos.fr" || a.Port != 465 || a.From != "hello@x.fr" ||
		a.FromName != "Hello" || !a.ImplicitTLS {
		t.Errorf("read back %+v", a)
	}
}

func TestAccountNeedsAHostAndAFrom(t *testing.T) {
	if _, err := mailer.AccountFromCreds(map[string]any{"from": "a@b.c"}); err == nil {
		t.Error("a connection with no host must not build an account")
	}
	if _, err := mailer.AccountFromCreds(map[string]any{"host": "h"}); err == nil {
		t.Error("a connection with no from address must not build an account")
	}
}

// A port stored as text by some form is common enough to accept.
func TestPortSurvivesItsStoredShape(t *testing.T) {
	for _, stored := range []any{float64(465), 465, "465"} {
		a, err := mailer.AccountFromCreds(map[string]any{"host": "h", "from": "a@b.c", "port": stored})
		if err != nil || a.Port != 465 {
			t.Errorf("port %v (%T) read back as %d (%v)", stored, stored, a.Port, err)
		}
	}
	a, _ := mailer.AccountFromCreds(map[string]any{"host": "h", "from": "a@b.c"})
	if a.Port != 587 {
		t.Errorf("an unset port should fall back to 587, got %d", a.Port)
	}
}

func TestAddressListDropsEmptyEntries(t *testing.T) {
	got := mailer.AddressList(" a@b.c , , d@e.f ")
	if len(got) != 2 || got[0] != "a@b.c" || got[1] != "d@e.f" {
		t.Errorf("got %v", got)
	}
}

func TestStripTags(t *testing.T) {
	if got := mailer.StripTags("<p>Bonjour <b>Marc</b></p>"); got != "Bonjour Marc" {
		t.Errorf("got %q", got)
	}
}

// RFC 3834: an automated sender says so, so an auto-responder does not answer
// it and two robots do not loop.
func TestHeadersAreWrittenVerbatim(t *testing.T) {
	body := compose(t, mailer.Message{Kind: "test",
		To:      []string{"to@example.com"},
		Subject: "Hello",
		Body:    "Hi.",
		Headers: map[string]string{"Auto-Submitted": "auto-generated", "X-AI-Generated": "true"},
	})
	for _, want := range []string{"Auto-Submitted: auto-generated\r\n", "X-AI-Generated: true\r\n"} {
		if !strings.Contains(body, want) {
			t.Errorf("the composed message does not carry %q", strings.TrimSpace(want))
		}
	}
}

// A CR or LF in a header would let whoever controls the value write headers of
// their own — a Bcc, or a second body.
func TestHeaderInjectionIsRefused(t *testing.T) {
	for name, h := range map[string]map[string]string{
		"LF in value":   {"X-Note": "ok\nBcc: victim@example.com"},
		"CR in value":   {"X-Note": "ok\rBcc: victim@example.com"},
		"CRLF in name":  {"X-Note\r\nBcc": "victim@example.com"},
		"colon in name": {"X-Note: x": "y"},
		"empty name":    {"": "y"},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := mailer.Compose(context.Background(), account(), mailer.Message{Kind: "test",
				To: []string{"to@example.com"}, Subject: "s", Body: "b", Headers: h,
			})
			if err == nil {
				t.Fatal("a header carrying a line break or a malformed name must be refused")
			}
		})
	}
}
