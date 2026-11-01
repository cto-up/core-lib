//go:build testutils

package core

import (
	"context"
	"crypto/rand"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/cto-up/core-lib/api/openapi/core"
	"github.com/cto-up/core-lib/pkg/core/db/repository"
	"github.com/cto-up/core-lib/pkg/core/db/testutils"
	coreservice "github.com/cto-up/core-lib/pkg/core/service"
	"github.com/cto-up/core-lib/pkg/shared/auth"
	"github.com/cto-up/core-lib/pkg/shared/mailer"
	sharedservice "github.com/cto-up/core-lib/pkg/shared/service"
)

func TestEmailDeliveryRights_Integration(t *testing.T) {
	store := testutils.NewTestStore(t)
	ctx := context.Background()
	key := make([]byte, 32)
	_, _ = rand.Read(key)
	require.NoError(t, mailer.SetDeliveryLog(store, key))

	exec := func(sql string, args ...any) {
		_, err := store.ConnPool.Exec(ctx, sql, args...)
		require.NoError(t, err)
	}
	suffix := uuid.NewString()[:8]
	corpa, corpb := "corpa-"+suffix, "corpb-"+suffix
	for _, id := range []string{corpa, corpb} {
		exec(`INSERT INTO core_tenants (tenant_id, name, subdomain, allow_password_sign_up, user_id)
			VALUES ($1, $1, $1, false, 'owner')`, id)
	}
	jane, admin := "jane-"+suffix, "admin-"+suffix
	janeEmail := jane + "@acme.com"
	for _, u := range []string{jane, admin} {
		exec(`INSERT INTO core_users (id, email, profile, roles) VALUES ($1, $2, '{}'::jsonb, '{}')`, u, u+"@acme.com")
	}
	for _, tnt := range []string{corpa, corpb} {
		exec(`INSERT INTO core_user_tenant_memberships (user_id, tenant_id, status, joined_at, roles)
			VALUES ($1, $2, 'active', NOW(), '{}')`, jane, tnt)
	}
	exec(`INSERT INTO core_user_tenant_memberships (user_id, tenant_id, status, joined_at, roles)
		VALUES ($1, $2, 'active', NOW(), '{ADMIN}')`, admin, corpa)

	insert := func(tenant, address string) {
		mac, err := mailer.RecipientHMAC(tenant, address)
		require.NoError(t, err)
		require.NoError(t, store.InsertEmailDelivery(ctx, repository.InsertEmailDeliveryParams{
			TenantID: pgtype.Text{String: tenant, Valid: tenant != ""}, Kind: "k", Account: "system",
			RecipientHmac: mac, RecipientMasked: mailer.MaskAddress(address), RecipientRole: "to", Status: mailer.StatusAccepted,
		}))
	}
	count := func(tenant, address string) int {
		mac, err := mailer.RecipientHMAC(tenant, address)
		require.NoError(t, err)
		rows, err := store.ListEmailDeliveriesForRecipient(ctx, repository.ListEmailDeliveriesForRecipientParams{
			TenantID: pgtype.Text{String: tenant, Valid: tenant != ""}, RecipientHmac: mac,
		})
		require.NoError(t, err)
		return len(rows)
	}
	seed := func() {
		exec(`DELETE FROM core_email_deliveries`)
		for i := 0; i < 3; i++ {
			insert(corpa, janeEmail)
			insert(corpb, janeEmail)
		}
		insert("", janeEmail)
		insert(corpa, "customer@client.com")
		insert(corpb, "customer@client.com")
	}
	contributor := coreservice.NewEmailDeliveriesContributor(store)

	t.Run("a member's export lists their rows in every tenant and platform mail", func(t *testing.T) {
		seed()
		out, err := contributor.Export(ctx, jane)
		require.NoError(t, err)
		rows := out.([]coreservice.EmailDeliveryExport)
		assert.Len(t, rows, 7)
		for _, r := range rows {
			assert.NotContains(t, r.Recipient, janeEmail, "the export shows the masked form only")
		}
	})

	t.Run("dormancy erases one tenant's rows, account deletion erases them all", func(t *testing.T) {
		seed()
		require.NoError(t, contributor.Erase(ctx, jane, corpa, sharedservice.EraseTenantDormancy))
		assert.Zero(t, count(corpa, janeEmail))
		assert.Equal(t, 3, count(corpb, janeEmail))
		assert.Equal(t, 1, count("", janeEmail))

		require.NoError(t, contributor.Erase(ctx, jane, "", sharedservice.EraseAccountDeletion))
		assert.Zero(t, count(corpb, janeEmail))
		assert.Zero(t, count("", janeEmail))
		assert.Equal(t, 1, count(corpa, "customer@client.com"), "other people's rows stay")
	})

	h := NewEmailDeliveryHandler(store)
	gin.SetMode(gin.TestMode)
	call := func(do func(*gin.Context)) *httptest.ResponseRecorder {
		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		c.Request = httptest.NewRequest(http.MethodGet, "/", nil)
		c.Set(auth.AUTH_TENANT_ID_KEY, corpa)
		c.Set(auth.AUTH_USER_ID, admin)
		c.Set(auth.AUTH_CLAIMS, map[string]interface{}{string(core.ADMIN): true})
		do(c)
		c.Writer.WriteHeaderNow()
		return w
	}

	t.Run("an admin erases a non-member's rows by address in their tenant only", func(t *testing.T) {
		seed()
		w := call(func(c *gin.Context) {
			h.EraseEmailDeliveries(c, core.EraseEmailDeliveriesParams{Address: "Customer <CUSTOMER@client.com>"})
		})
		assert.Equal(t, http.StatusNoContent, w.Code)
		assert.Zero(t, count(corpa, "customer@client.com"))
		assert.Equal(t, 1, count(corpb, "customer@client.com"))
	})

	t.Run("searching a member's address is recorded as a read of their data", func(t *testing.T) {
		seed()
		sharedservice.SetPersonalDataAccessStore(store)
		t.Cleanup(func() { sharedservice.SetPersonalDataAccessStore(nil) })
		exec(`DELETE FROM core_personal_data_access_log WHERE tenant_id = $1`, corpa)

		member, outsider := janeEmail, "customer@client.com"
		call(func(c *gin.Context) { h.ListEmailDeliveries(c, core.ListEmailDeliveriesParams{Address: &outsider}) })
		call(func(c *gin.Context) { h.ListEmailDeliveries(c, core.ListEmailDeliveriesParams{Address: &member}) })

		require.Eventually(t, func() bool {
			rows, err := store.ListPersonalDataAccess(ctx, repository.ListPersonalDataAccessParams{
				TenantID: pgtype.Text{String: corpa, Valid: true}, RowLimit: 10,
			})
			require.NoError(t, err)
			return len(rows) >= 1
		}, 5*time.Second, 20*time.Millisecond)
		// The writer is asynchronous; give a wrongly logged outsider search time to land.
		time.Sleep(200 * time.Millisecond)
		rows, err := store.ListPersonalDataAccess(ctx, repository.ListPersonalDataAccessParams{
			TenantID: pgtype.Text{String: corpa, Valid: true}, RowLimit: 10,
		})
		require.NoError(t, err)
		require.Len(t, rows, 1, "only the member's address names someone")
		assert.Equal(t, admin, rows[0].ActorUserID)
		assert.Equal(t, jane, rows[0].SubjectUserID)
		assert.Equal(t, "email_deliveries.search", rows[0].Resource)
	})
}
