package mailer

import (
	"context"
	"crypto/hkdf"
	"crypto/hmac"
	"crypto/sha256"
	"errors"
	"fmt"
	netmail "net/mail"
	"strings"
	"sync/atomic"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/wneessen/go-mail"

	"github.com/cto-up/core-lib/pkg/core/db"
	"github.com/cto-up/core-lib/pkg/core/db/repository"
	"github.com/cto-up/core-lib/pkg/shared/util"
)

// Delivery statuses (ADR 090 §3). accepted is "the relay took it", not
// "it was delivered": nothing after the relay is known.
const (
	StatusAccepted = "accepted"
	StatusRejected = "rejected"
	StatusFailed   = "failed"
)

// DeliveryRetention is how long a delivery row is kept.
const DeliveryRetention = 90 * 24 * time.Hour

type deliveryLog struct {
	store *db.Store
	key   []byte
}

var installed atomic.Pointer[deliveryLog]

var (
	sendsTotal = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "email_sends_total",
		Help: "Emails sent, by kind and outcome. One per message, not per recipient.",
	}, []string{"kind", "status"})
	logWriteFailures = prometheus.NewCounter(prometheus.CounterOpts{
		Name: "email_delivery_log_write_failures_total",
		Help: "Delivery-log rows that could not be written. The send itself is unaffected.",
	})
)

func init() {
	prometheus.MustRegister(sendsTotal, logWriteFailures)
}

// SetDeliveryLog points Send at the delivery log. Called once at boot with the
// core store and the decoded MAIL_RECIPIENT_KEY. Until it is called, Send
// sends and records nothing — which is what a unit test wants.
func SetDeliveryLog(store *db.Store, key []byte) error {
	if store == nil {
		return errors.New("mailer: the delivery log needs a store")
	}
	if len(key) != 32 {
		return fmt.Errorf("mailer: the recipient key must be 32 bytes, got %d", len(key))
	}
	installed.Store(&deliveryLog{store: store, key: key})
	return nil
}

// RecipientHMAC is the value the log stores for address in tenantID's rows.
// An exact address typed by an administrator is matched through it.
func RecipientHMAC(tenantID, address string) ([]byte, error) {
	l := installed.Load()
	if l == nil {
		return nil, errors.New("mailer: the delivery log is not configured")
	}
	return recipientHMAC(l.key, tenantID, address)
}

func recipientHMAC(secret []byte, tenantID, address string) ([]byte, error) {
	key, err := hkdf.Key(sha256.New, secret, []byte(tenantID), "email-recipient", 32)
	if err != nil {
		return nil, err
	}
	mac := hmac.New(sha256.New, key)
	mac.Write([]byte(NormalizeAddress(address)))
	return mac.Sum(nil), nil
}

// NormalizeAddress strips a display name and case, so "Ana <ANA@x.test>" and
// "ana@x.test" are the same recipient.
func NormalizeAddress(address string) string {
	if parsed, err := netmail.ParseAddress(address); err == nil {
		address = parsed.Address
	}
	return strings.ToLower(strings.TrimSpace(address))
}

// MaskAddress keeps the first character of the local part and the domain:
// "jane@acme.com" → "j***@acme.com".
func MaskAddress(address string) string {
	addr := NormalizeAddress(address)
	local, domain, ok := strings.Cut(addr, "@")
	if !ok || local == "" {
		return "***"
	}
	return local[:1] + "***@" + domain
}

type outcome struct {
	status     string
	smtpCode   int
	errorClass string
}

// classify reads what Send's error says about the relay's answer.
func classify(err error, composeFailed bool) outcome {
	if err == nil {
		return outcome{status: StatusAccepted}
	}
	if composeFailed {
		return outcome{status: StatusFailed, errorClass: "compose"}
	}
	var se *mail.SendError
	if errors.As(err, &se) {
		switch se.Reason {
		case mail.ErrSMTPMailFrom, mail.ErrSMTPRcptTo, mail.ErrSMTPData, mail.ErrSMTPDataClose:
			return outcome{status: StatusRejected, smtpCode: se.ErrorCode(), errorClass: rejectedClass(se.Reason)}
		}
		return outcome{status: StatusFailed, smtpCode: se.ErrorCode(), errorClass: "connection"}
	}
	msg := strings.ToLower(err.Error())
	switch {
	case errors.Is(err, context.DeadlineExceeded), errors.Is(err, context.Canceled), strings.Contains(msg, "timeout"):
		return outcome{status: StatusFailed, errorClass: "timeout"}
	case strings.Contains(msg, "tls"):
		return outcome{status: StatusFailed, errorClass: "tls"}
	case strings.Contains(msg, "auth"):
		return outcome{status: StatusFailed, errorClass: "auth"}
	}
	return outcome{status: StatusFailed, errorClass: "dial"}
}

func rejectedClass(r mail.SendErrReason) string {
	switch r {
	case mail.ErrSMTPMailFrom:
		return "sender_rejected"
	case mail.ErrSMTPRcptTo:
		return "recipient_rejected"
	}
	return "message_rejected"
}

// record writes one row per recipient. It never changes what Send returns: a
// failed write is logged and counted, nothing more.
func record(ctx context.Context, a Account, m Message, messageID string, sendErr error, composeFailed bool) {
	o := classify(sendErr, composeFailed)
	sendsTotal.WithLabelValues(m.Kind, o.status).Inc()

	l := installed.Load()
	if l == nil {
		return
	}
	tenantID := m.TenantID
	account := a.Ref
	if account == "" {
		account = "connection"
	}

	writeCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	logger := util.GetLoggerFromCtx(ctx)
	q := l.store.Queries

	for _, r := range recipients(m) {
		mac, err := recipientHMAC(l.key, tenantID, r.address)
		if err == nil {
			err = q.InsertEmailDelivery(writeCtx, repository.InsertEmailDeliveryParams{
				TenantID:        pgtype.Text{String: tenantID, Valid: tenantID != ""},
				Kind:            m.Kind,
				OriginType:      pgtype.Text{String: m.Origin.Type, Valid: m.Origin.Type != "" && m.Origin.ID != ""},
				OriginID:        pgtype.Text{String: m.Origin.ID, Valid: m.Origin.Type != "" && m.Origin.ID != ""},
				Account:         account,
				RecipientHmac:   mac,
				RecipientMasked: MaskAddress(r.address),
				RecipientRole:   r.role,
				Status:          o.status,
				SmtpCode:        pgtype.Int4{Int32: int32(o.smtpCode), Valid: o.smtpCode > 0},
				ErrorClass:      pgtype.Text{String: o.errorClass, Valid: o.errorClass != ""},
				MessageID:       pgtype.Text{String: messageID, Valid: messageID != "" && o.status == StatusAccepted},
			})
		}
		if err != nil {
			logWriteFailures.Inc()
			logger.Err(err).Str("kind", m.Kind).Msg("mailer: delivery log row not written")
		}
	}
}

type recipient struct{ address, role string }

func recipients(m Message) []recipient {
	out := make([]recipient, 0, len(m.To)+len(m.Cc)+len(m.Bcc))
	for _, set := range []struct {
		role  string
		addrs []string
	}{{"to", m.To}, {"cc", m.Cc}, {"bcc", m.Bcc}} {
		for _, a := range set.addrs {
			out = append(out, recipient{address: a, role: set.role})
		}
	}
	return out
}
