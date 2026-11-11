package kratos

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	ory "github.com/ory/kratos-client-go"

	"github.com/cto-up/core-lib/pkg/shared/auth"
)

// A signed-in user on a tenant they do not belong to was refused every call,
// including the one that lists their own organizations, so the app could only
// show "Access Required" instead of sending them to a tenant of theirs.

type noTenancy struct{}

func (noTenancy) GetTenantIDWithSubdomain(context.Context, string) (string, error) { return "", nil }
func (noTenancy) IsReseller(context.Context, string) (bool, error)                 { return false, nil }
func (noTenancy) IsActingResellerFor(context.Context, string, string) (bool, error) {
	return false, nil
}
func (noTenancy) GetTenantAllowSignUp(context.Context, string) (bool, error) { return false, nil }

func nonMemberProvider(t *testing.T) *KratosAuthProvider {
	session := `{"id":"5f2a1c88-0000-4000-8000-000000000001","active":true,"identity":` +
		`{"id":"9d4b7e10-0000-4000-8000-000000000002","schema_id":"default",` +
		`"schema_url":"http://kratos.test/schemas/default","state":"active",` +
		`"traits":{"email":"who@example.com"},` +
		`"metadata_public":{"tenant_memberships":[{"tenant_id":"other-tenant","roles":["USER"]}]}}}`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(session))
	}))
	t.Cleanup(srv.Close)
	cfg := ory.NewConfiguration()
	cfg.Servers = ory.ServerConfigurations{{URL: srv.URL}}
	client := ory.NewAPIClient(cfg)
	return &KratosAuthProvider{adminClient: client, publicClient: client, multitenantService: noTenancy{}}
}

func TestNonMemberMayListTheirOwnTenants(t *testing.T) {
	ctx := context.WithValue(context.Background(), auth.REQUEST_URL_PATH, "/api/v1/users/me/tenants")
	user, err := nonMemberProvider(t).VerifyTokenWithTenantID(ctx, "this-tenant", "cookie")
	if err != nil {
		t.Fatalf("listing one's own tenants was refused: %v", err)
	}
	if len(user.TenantMemberships) != 0 || user.Claims["USER"] == true {
		t.Errorf("a non-member must carry no role on this tenant, got %+v / %+v", user.TenantMemberships, user.Claims)
	}
}

func TestNonMemberIsStillRefusedEverythingElse(t *testing.T) {
	for _, path := range []string{
		"/api/v1/users/me",
		"/api/v1/users/me/tenants/pending",
		"/api/v1/users/me/tenants/this-tenant/accept",
		"/api/v1/agents",
	} {
		ctx := context.WithValue(context.Background(), auth.REQUEST_URL_PATH, path)
		if _, err := nonMemberProvider(t).VerifyTokenWithTenantID(ctx, "this-tenant", "cookie"); err == nil {
			t.Errorf("%s: a non-member was let in", path)
		}
	}
}
