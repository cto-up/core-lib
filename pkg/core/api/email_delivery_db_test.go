//go:build testutils

package core

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/cto-up/core-lib/api/openapi/core"
	"github.com/cto-up/core-lib/pkg/core/db/repository"
	"github.com/cto-up/core-lib/pkg/core/db/testutils"
	"github.com/cto-up/core-lib/pkg/shared/auth"
	"github.com/cto-up/core-lib/pkg/shared/mailer"
)

func TestEmailDeliveryHandler_Integration(t *testing.T) {
	store := testutils.NewTestStore(t)
	ctx := context.Background()
	key := make([]byte, 32)
	_, _ = rand.Read(key)
	require.NoError(t, mailer.SetDeliveryLog(store, key))
	_, err := store.ConnPool.Exec(ctx, `DELETE FROM core_email_deliveries`)
	require.NoError(t, err)

	insert := func(tenant, kind, status, address string) {
		mac, err := mailer.RecipientHMAC(tenant, address)
		require.NoError(t, err)
		require.NoError(t, store.InsertEmailDelivery(ctx, repository.InsertEmailDeliveryParams{
			TenantID: pgtype.Text{String: tenant, Valid: tenant != ""}, Kind: kind, Account: "system",
			RecipientHmac: mac, RecipientMasked: mailer.MaskAddress(address), RecipientRole: "to", Status: status,
			SmtpCode:   pgtype.Int4{Int32: 550, Valid: status == mailer.StatusRejected},
			ErrorClass: pgtype.Text{String: "recipient_rejected", Valid: status == mailer.StatusRejected},
		}))
	}
	insert("corpa", "agent.send_email", mailer.StatusAccepted, "jane@acme.com")
	insert("corpa", "agent.send_email", mailer.StatusRejected, "bob@acme.com")
	insert("corpa", "core.password_reset", mailer.StatusFailed, "jane@acme.com")
	insert("corpb", "agent.send_email", mailer.StatusAccepted, "jane@acme.com")
	insert("", "core.inactive_warning", mailer.StatusAccepted, "jane@acme.com")

	h := NewEmailDeliveryHandler(store)
	gin.SetMode(gin.TestMode)
	call := func(tenantID string, superAdmin bool, do func(*gin.Context)) *httptest.ResponseRecorder {
		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		c.Request = httptest.NewRequest(http.MethodGet, "/", nil)
		c.Set(auth.AUTH_TENANT_ID_KEY, tenantID)
		c.Set(auth.AUTH_CLAIMS, map[string]interface{}{string(core.SUPERADMIN): superAdmin, string(core.ADMIN): true})
		do(c)
		return w
	}
	list := func(tenantID string, p core.ListEmailDeliveriesParams) (int, []core.EmailDelivery) {
		w := call(tenantID, false, func(c *gin.Context) { h.ListEmailDeliveries(c, p) })
		var out []core.EmailDelivery
		if w.Code == http.StatusOK {
			require.NoError(t, json.Unmarshal(w.Body.Bytes(), &out))
		}
		return w.Code, out
	}

	t.Run("an admin sees only their tenant's rows, masked", func(t *testing.T) {
		code, rows := list("corpa", core.ListEmailDeliveriesParams{})
		require.Equal(t, http.StatusOK, code)
		require.Len(t, rows, 3)
		raw := call("corpa", false, func(c *gin.Context) { h.ListEmailDeliveries(c, core.ListEmailDeliveriesParams{}) }).Body.String()
		assert.NotContains(t, raw, "jane@acme.com")
		assert.NotContains(t, raw, "corpb")
		_, other := list("corpb", core.ListEmailDeliveriesParams{})
		assert.Len(t, other, 1, "corpa's rows never appear for corpb")
	})

	t.Run("status and kind filter", func(t *testing.T) {
		failed := core.EmailDeliveryStatusRejected
		_, rows := list("corpa", core.ListEmailDeliveriesParams{Status: &failed})
		require.Len(t, rows, 1)
		assert.Equal(t, "b***@acme.com", rows[0].RecipientMasked)
		assert.EqualValues(t, 550, *rows[0].SmtpCode)
		assert.Equal(t, "recipient_rejected", *rows[0].ErrorClass)

		kind := "core.password_reset"
		_, rows = list("corpa", core.ListEmailDeliveriesParams{Kind: &kind})
		require.Len(t, rows, 1)
	})

	t.Run("an exact address matches whatever its case or display name", func(t *testing.T) {
		addr := "Jane <JANE@acme.com>"
		_, rows := list("corpa", core.ListEmailDeliveriesParams{Address: &addr})
		assert.Len(t, rows, 2)
		other := "nobody@acme.com"
		_, rows = list("corpa", core.ListEmailDeliveriesParams{Address: &other})
		assert.Empty(t, rows)
	})

	t.Run("no tenant is refused", func(t *testing.T) {
		code, _ := list("", core.ListEmailDeliveriesParams{})
		assert.Equal(t, http.StatusForbidden, code)
	})

	t.Run("the super-admin summary counts every tenant and names none", func(t *testing.T) {
		w := call("", true, func(c *gin.Context) { h.SummarizeEmailDeliveries(c, core.SummarizeEmailDeliveriesParams{}) })
		require.Equal(t, http.StatusOK, w.Code)
		assert.NotContains(t, w.Body.String(), "corpa")
		assert.NotContains(t, w.Body.String(), "***")
		var counts []core.EmailDeliveryCount
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &counts))
		got := map[string]int64{}
		for _, c := range counts {
			got[c.Kind+"/"+string(c.Status)] = c.Count
		}
		assert.Equal(t, map[string]int64{
			"agent.send_email/accepted": 2, "agent.send_email/rejected": 1,
			"core.password_reset/failed": 1, "core.inactive_warning/accepted": 1,
		}, got)

		w = call("corpa", false, func(c *gin.Context) { h.SummarizeEmailDeliveries(c, core.SummarizeEmailDeliveriesParams{}) })
		assert.Equal(t, http.StatusForbidden, w.Code)
	})
}
