package mailer_test

import (
	"context"
	"testing"

	"github.com/cto-up/core-lib/pkg/shared/config"
	"github.com/cto-up/core-lib/pkg/shared/mailer"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSystemAccountReadsTheRelay(t *testing.T) {
	prev := config.Current()
	t.Cleanup(func() { config.Set(prev) })
	for port, implicit := range map[string]bool{"465": true, " 465 ": true, "587": false, "25": false} {
		c := prev
		c.SMTP = config.SMTP{Host: "smtp.acme.test", Port: port, Username: "mailer", Password: "secret"}
		c.Email.SystemFrom = "alerts@acme.test"
		config.Set(c)
		a := mailer.SystemAccount()
		assert.Equal(t, "smtp.acme.test", a.Host)
		assert.Equal(t, implicit, a.ImplicitTLS, "port %q", port)
		assert.Equal(t, "alerts@acme.test", a.From)
		assert.Equal(t, "mailer", a.Username)
	}
}

func TestAMessageWithoutAKindIsRefused(t *testing.T) {
	_, err := mailer.Compose(context.Background(), account(), mailer.Message{To: []string{"to@example.com"}, Subject: "s"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "kind")
}
