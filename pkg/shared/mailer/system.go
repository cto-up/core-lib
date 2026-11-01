package mailer

import (
	"strconv"
	"strings"

	"github.com/cto-up/core-lib/pkg/shared/config"
)

// SystemAccount is the platform relay (SMTP_*), sending as the system address.
// Port 465 is implicit TLS; every other port is STARTTLS, which is mandatory.
func SystemAccount() Account {
	s := config.SMTPSettings()
	port, err := strconv.Atoi(strings.TrimSpace(s.Port))
	if err != nil || port <= 0 {
		port = 587
	}
	return Account{
		Host:        strings.TrimSpace(s.Host),
		Port:        port,
		Username:    s.Username,
		Password:    s.Password,
		From:        config.SystemEmailFrom(),
		ImplicitTLS: port == 465,
	}
}
