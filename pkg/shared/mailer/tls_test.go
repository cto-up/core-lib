package mailer_test

import (
	"bufio"
	"context"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/cto-up/core-lib/pkg/shared/mailer"
	"github.com/stretchr/testify/require"
)

// plainRelay speaks SMTP but never offers STARTTLS, like a development sink.
// It records whether a message body ever reached it.
func plainRelay(t *testing.T) (port int, gotData chan bool) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	t.Cleanup(func() { _ = ln.Close() })
	gotData = make(chan bool, 1)
	go func() {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		_ = conn.SetDeadline(time.Now().Add(5 * time.Second))
		w := bufio.NewWriter(conn)
		say := func(s string) { _, _ = w.WriteString(s + "\r\n"); _ = w.Flush() }
		say("220 plain.test ESMTP")
		r := bufio.NewReader(conn)
		data := false
		for {
			line, err := r.ReadString('\n')
			if err != nil {
				gotData <- data
				return
			}
			switch cmd := strings.ToUpper(strings.TrimSpace(line)); {
			case strings.HasPrefix(cmd, "EHLO"):
				say("250-plain.test")
				say("250 8BITMIME")
			case strings.HasPrefix(cmd, "DATA"):
				data = true
				say("354 go ahead")
			case strings.HasPrefix(cmd, "QUIT"):
				say("221 bye")
				gotData <- data
				return
			default:
				say("250 ok")
			}
		}
	}()
	return ln.Addr().(*net.TCPAddr).Port, gotData
}

func TestARelayWithoutSTARTTLSIsRefused(t *testing.T) {
	port, gotData := plainRelay(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	err := mailer.Send(ctx, mailer.Account{Host: "127.0.0.1", Port: port, From: "noreply@acme.test"},
		mailer.Message{Kind: "core.password_reset", To: []string{"ada@acme.test"}, Subject: "s", Body: "b"})

	require.Error(t, err, "a relay that cannot encrypt must not receive the message in clear")
	require.Contains(t, err.Error(), "STARTTLS")
	select {
	case sent := <-gotData:
		require.False(t, sent, "the message body reached a plaintext relay")
	case <-time.After(500 * time.Millisecond):
	}
}
