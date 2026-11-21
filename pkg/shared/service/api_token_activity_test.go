//go:build testutils

package service

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"

	"github.com/cto-up/core-lib/pkg/core/db/testutils"
	"github.com/cto-up/core-lib/pkg/shared/access"
	"github.com/cto-up/core-lib/pkg/shared/activity"
)

// office#200: a token's created, used and revoked rows are activity in its
// tenant, with who did it; deleting the token keeps them.
func TestATokensEventsAreActivityThatOutlivesTheToken_Integration(t *testing.T) {
	store := testutils.NewTestStore(t)
	svc := NewClientApplicationService(store)
	app := createTestClientApplication(t, svc)
	tenantID := app.TenantID.String
	_, err := store.ConnPool.Exec(context.Background(), `INSERT INTO core_users (id, email, profile) VALUES ('admin-1', 'admin@acme.com', '{"name": "Ada"}')`)
	require.NoError(t, err)
	as := func(user string) *gin.Context {
		c, _ := gin.CreateTestContext(httptest.NewRecorder())
		c.Request = httptest.NewRequest(http.MethodPost, "/", nil).WithContext(access.WithPrincipal(context.Background(),
			access.Principal{TenantID: tenantID, UserID: user, Door: access.DoorWeb}))
		c.Request.Header.Set("User-Agent", "test-agent")
		return c
	}

	secret, tok, err := svc.CreateAPIToken(as("admin-1"), app.ID, tenantID, "ci", "", 30, "admin-1", []string{"read"})
	require.NoError(t, err)
	_, err = svc.VerifyAPIToken(as(""), secret)
	require.NoError(t, err)
	_, err = svc.RevokeAPIToken(as("admin-1"), tok.ID, tenantID, "leaked", "admin-1")
	require.NoError(t, err)

	rows, err := svc.GetAPITokenAuditLogs(context.Background(), tenantID, tok.ID, 20, 0)
	require.NoError(t, err)
	require.Len(t, rows, 3)
	require.Equal(t, []string{"api_token.revoked", "api_token.used", "api_token.created"}, []string{rows[0].Action, rows[1].Action, rows[2].Action})
	require.Equal(t, "admin-1", rows[0].ActorID, "who revoked it")
	require.Equal(t, "Ada", rows[0].ActorName)
	require.Equal(t, access.DoorWeb, rows[0].Via)
	require.Equal(t, access.DoorAPIKey, rows[1].Via, "a use is the token itself")
	require.Equal(t, tok.ID.String(), rows[1].KeyID)
	require.Equal(t, "admin-1", rows[1].ActorID, "acting as its creator")
	require.Equal(t, "test-agent", rows[2].Changes["userAgent"][1])
	require.Equal(t, &activity.Ref{Kind: "client_application", ID: app.ID.String()}, rows[2].Scope)
	require.Equal(t, TokenAuditRevoked, TokenAuditAction(rows[0].Action))

	page2, err := svc.GetAPITokenAuditLogs(context.Background(), tenantID, tok.ID, 2, 2)
	require.NoError(t, err)
	require.Len(t, page2, 1, "the endpoint's page and pageSize still page")

	require.NoError(t, svc.DeleteAPIToken(context.Background(), tok.ID))
	rows, err = svc.GetAPITokenAuditLogs(context.Background(), tenantID, tok.ID, 20, 0)
	require.NoError(t, err)
	require.Len(t, rows, 3, "deleting the token keeps what it did")

	var n int
	require.NoError(t, store.ConnPool.QueryRow(context.Background(),
		`SELECT count(*) FROM pg_tables WHERE tablename = 'core_api_token_audit_logs'`).Scan(&n))
	require.Zero(t, n, "the old table is gone")

	ok, err := APITokenKind{}.CanRead(context.Background(), tenantID, access.Principal{TenantID: tenantID, Roles: []string{access.RoleAdmin}}, tok.ID.String())
	require.NoError(t, err)
	require.True(t, ok)
	ok, _ = APITokenKind{}.CanRead(context.Background(), tenantID, access.Principal{TenantID: tenantID}, tok.ID.String())
	require.False(t, ok, "a member is not an administrator")
	ok, _ = APITokenKind{}.CanRead(context.Background(), "", access.Principal{Roles: []string{access.RoleAdmin}}, tok.ID.String())
	require.False(t, ok, "a global application's tokens: super administrators only")
}

func TestAGlobalApplicationsTokenIsThePlatformsActivity_Integration(t *testing.T) {
	store := testutils.NewTestStore(t)
	svc := NewClientApplicationService(store)
	app, err := svc.CreateClientApplication(&gin.Context{}, "", "global", "", "superadmin-1")
	require.NoError(t, err)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/", nil)
	_, tok, err := svc.CreateAPIToken(c, app.ID, "", "ops", "", 30, "superadmin-1", nil)
	require.NoError(t, err)
	rows, err := svc.GetAPITokenAuditLogs(context.Background(), "", tok.ID, 20, 0)
	require.NoError(t, err)
	require.Len(t, rows, 1)
	var tenant *string
	require.NoError(t, store.ConnPool.QueryRow(context.Background(), `SELECT tenant_id FROM core_activity WHERE id = $1`, rows[0].ID).Scan(&tenant))
	require.Nil(t, tenant, "no tenant: the platform's")
	require.Error(t, activity.Record(context.Background(), store.ConnPool, activity.Event{Action: "x.y", Target: activity.Ref{Kind: "x", ID: "1"}}),
		"a forgotten tenant is still refused")
}
