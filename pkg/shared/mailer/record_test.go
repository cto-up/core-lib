//go:build testutils

package mailer

import (
	"context"
	"crypto/rand"
	"net"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus/testutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/cto-up/core-lib/pkg/core/db"
	"github.com/cto-up/core-lib/pkg/core/db/testutils"
)

type deliveryRow struct {
	TenantID, Kind, OriginType, OriginID, Account *string
	Masked, Role, Status                          string
	SMTPCode                                      *int32
	ErrorClass, MessageID                         *string
	HMAC                                          []byte
}

func installLog(t *testing.T, store *db.Store) []byte {
	t.Helper()
	key := make([]byte, 32)
	_, _ = rand.Read(key)
	require.NoError(t, SetDeliveryLog(store, key))
	t.Cleanup(func() { installed.Store(nil) })
	_, err := store.ConnPool.Exec(context.Background(), `DELETE FROM core_email_deliveries`)
	require.NoError(t, err)
	return key
}

func deliveryRows(t *testing.T, store *db.Store) []deliveryRow {
	t.Helper()
	rows, err := store.ConnPool.Query(context.Background(), `SELECT tenant_id, kind, origin_type, origin_id, account,
		recipient_masked, recipient_role, status, smtp_code, error_class, message_id, recipient_hmac
		FROM core_email_deliveries ORDER BY recipient_role DESC, recipient_masked`)
	require.NoError(t, err)
	defer rows.Close()
	var out []deliveryRow
	for rows.Next() {
		var r deliveryRow
		require.NoError(t, rows.Scan(&r.TenantID, &r.Kind, &r.OriginType, &r.OriginID, &r.Account,
			&r.Masked, &r.Role, &r.Status, &r.SMTPCode, &r.ErrorClass, &r.MessageID, &r.HMAC))
		out = append(out, r)
	}
	return out
}

func TestDeliveryLog_Integration(t *testing.T) {
	store := testutils.NewTestStore(t)

	t.Run("an accepted mail leaves one row per recipient and no plain address", func(t *testing.T) {
		key := installLog(t, store)
		m := Message{
			Kind: "agent.send_email", TenantID: "corpa",
			Origin: Origin{Type: OriginAgentRun, ID: "run-1"},
			To:     []string{"Jane <Jane@acme.com>"}, Cc: []string{"bob@acme.com"},
			Subject: "Your contract for Jane", Body: "secret body",
		}
		record(context.Background(), Account{Ref: "conn-1"}, m, "<abc@relay>", nil, false)

		rows := deliveryRows(t, store)
		require.Len(t, rows, 2)
		to, cc := rows[0], rows[1]
		assert.Equal(t, "to", to.Role)
		assert.Equal(t, "j***@acme.com", to.Masked)
		assert.Equal(t, "cc", cc.Role)
		assert.Equal(t, "b***@acme.com", cc.Masked)
		for _, r := range rows {
			assert.Equal(t, "corpa", *r.TenantID)
			assert.Equal(t, "agent.send_email", *r.Kind)
			assert.Equal(t, OriginAgentRun, *r.OriginType)
			assert.Equal(t, "run-1", *r.OriginID)
			assert.Equal(t, "conn-1", *r.Account)
			assert.Equal(t, StatusAccepted, r.Status)
			assert.Equal(t, "<abc@relay>", *r.MessageID)
			assert.Nil(t, r.ErrorClass)
		}
		want, err := recipientHMAC(key, "corpa", "jane@acme.com")
		require.NoError(t, err)
		assert.Equal(t, want, to.HMAC, "the display name and case must not change the match")

		var leaked int
		require.NoError(t, store.ConnPool.QueryRow(context.Background(),
			`SELECT count(*) FROM core_email_deliveries t WHERE t::text ILIKE '%jane@acme.com%' OR t::text ILIKE '%contract%' OR t::text ILIKE '%secret body%'`).Scan(&leaked))
		assert.Zero(t, leaked, "no column may hold the plain address, the subject or the body")
	})

	t.Run("an unreachable relay is a failed row and Send's error is unchanged", func(t *testing.T) {
		installLog(t, store)
		ln, err := net.Listen("tcp", "127.0.0.1:0")
		require.NoError(t, err)
		port := ln.Addr().(*net.TCPAddr).Port
		require.NoError(t, ln.Close())

		sendErr := Send(context.Background(), Account{Host: "127.0.0.1", Port: port, From: "noreply@acme.test", Ref: SystemRef},
			Message{Kind: "core.password_reset", TenantID: "corpa", To: []string{"ada@acme.test"}, Subject: "s", Body: "b"})
		require.Error(t, sendErr)

		rows := deliveryRows(t, store)
		require.Len(t, rows, 1)
		r := rows[0]
		assert.Equal(t, StatusFailed, r.Status)
		assert.Equal(t, "dial", *r.ErrorClass)
		assert.Nil(t, r.SMTPCode)
		assert.Nil(t, r.MessageID)
		assert.Equal(t, "corpa", *r.TenantID)
		assert.Equal(t, SystemRef, *r.Account)
	})

	t.Run("platform mail with no tenant is stored with a NULL tenant", func(t *testing.T) {
		installLog(t, store)
		record(context.Background(), Account{Ref: SystemRef},
			Message{Kind: "core.inactive_warning", To: []string{"ada@acme.test"}}, "", nil, false)
		rows := deliveryRows(t, store)
		require.Len(t, rows, 1)
		assert.Nil(t, rows[0].TenantID)
	})

	t.Run("a failed log write is counted and does not change the send result", func(t *testing.T) {
		installLog(t, store)
		_, err := store.ConnPool.Exec(context.Background(), `ALTER TABLE core_email_deliveries RENAME TO core_email_deliveries_off`)
		require.NoError(t, err)
		t.Cleanup(func() {
			_, _ = store.ConnPool.Exec(context.Background(), `ALTER TABLE core_email_deliveries_off RENAME TO core_email_deliveries`)
		})

		before := testutil.ToFloat64(logWriteFailures)
		composeErr := Send(context.Background(), Account{Host: "h", From: "noreply@acme.test"},
			Message{To: []string{"ada@acme.test"}})
		require.Error(t, composeErr)
		assert.Contains(t, composeErr.Error(), "kind", "Send returns its own error, not the log's")
		assert.Equal(t, before+1, testutil.ToFloat64(logWriteFailures))
	})

	t.Run("the sweep deletes rows older than the retention", func(t *testing.T) {
		installLog(t, store)
		ctx := context.Background()
		_, err := store.ConnPool.Exec(ctx, `INSERT INTO core_email_deliveries
			(created_at, kind, account, recipient_hmac, recipient_masked, recipient_role, status) VALUES
			(now() - interval '91 days', 'k', 'system', '\x00', 'a***@x', 'to', 'accepted'),
			(now() - interval '89 days', 'k', 'system', '\x00', 'b***@x', 'to', 'accepted')`)
		require.NoError(t, err)
		n, err := store.Queries.PurgeEmailDeliveriesBefore(ctx, time.Now().Add(-DeliveryRetention))
		require.NoError(t, err)
		assert.EqualValues(t, 1, n)
		rows := deliveryRows(t, store)
		require.Len(t, rows, 1)
		assert.Equal(t, "b***@x", rows[0].Masked)
	})
}
