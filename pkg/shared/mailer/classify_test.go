package mailer

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/wneessen/go-mail"
)

func TestMaskAddress(t *testing.T) {
	for in, want := range map[string]string{
		"jane@acme.com":            "j***@acme.com",
		"Jane Doe <JANE@Acme.com>": "j***@acme.com",
		"no-at-sign":               "***",
		"":                         "***",
	} {
		assert.Equal(t, want, MaskAddress(in), in)
	}
}

func TestRecipientHMACIsPerTenantAndCaseBlind(t *testing.T) {
	key := make([]byte, 32)
	a, _ := recipientHMAC(key, "corpa", "Jane <JANE@acme.com>")
	b, _ := recipientHMAC(key, "corpa", "jane@acme.com")
	c, _ := recipientHMAC(key, "corpb", "jane@acme.com")
	assert.Equal(t, a, b)
	assert.NotEqual(t, a, c, "the same address must not match across tenants")
}

func TestClassify(t *testing.T) {
	assert.Equal(t, outcome{status: StatusAccepted}, classify(nil, false))
	assert.Equal(t, "compose", classify(errors.New("bad"), true).errorClass)
	assert.Equal(t, "timeout", classify(fmt.Errorf("x: %w", context.DeadlineExceeded), false).errorClass)
	assert.Equal(t, "tls", classify(errors.New(`STARTTLS mode set to: "TLSMandatory"`), false).errorClass)
	assert.Equal(t, "dial", classify(errors.New("dial tcp: connection refused"), false).errorClass)

	rcpt := classify(&mail.SendError{Reason: mail.ErrSMTPRcptTo}, false)
	assert.Equal(t, StatusRejected, rcpt.status)
	assert.Equal(t, "recipient_rejected", rcpt.errorClass)
	assert.Equal(t, StatusFailed, classify(&mail.SendError{Reason: mail.ErrConnCheck}, false).status)
}
