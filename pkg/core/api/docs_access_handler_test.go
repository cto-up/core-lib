package core

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/cto-up/core-lib/pkg/shared/auth"
	"github.com/gin-gonic/gin"
)

type fakeVerifier struct {
	token *auth.Token
	err   error
}

func (f fakeVerifier) VerifySessionCredential(context.Context, auth.SessionCredential) (*auth.Token, error) {
	return f.token, f.err
}

func membership(roles ...string) []interface{} {
	rs := make([]interface{}, len(roles))
	for i, r := range roles {
		rs[i] = r
	}
	return []interface{}{map[string]interface{}{"tenant_id": "t1", "roles": rs}}
}

func askDocs(t *testing.T, v SessionVerifier, site string, withCookie bool) *httptest.ResponseRecorder {
	t.Helper()
	gin.SetMode(gin.TestMode)
	r := gin.New()
	RegisterDocsAccessRoute(r, NewDocsAccessHandler(v))
	req := httptest.NewRequest(http.MethodGet, DocsAccessPath+"?site="+site, nil)
	if withCookie {
		req.AddCookie(&http.Cookie{Name: "ory_kratos_session", Value: "s"})
	}
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w
}

func TestDocsAccess(t *testing.T) {
	customerAdmin := fakeVerifier{token: &auth.Token{UID: "u", Claims: map[string]interface{}{
		auth.AUTH_TENANT_MEMBERSHIPS: membership("CUSTOMER_ADMIN"),
	}}}
	globalAdmin := fakeVerifier{token: &auth.Token{UID: "u", Claims: map[string]interface{}{
		"global_roles": []interface{}{"ADMIN"},
	}}}
	membershipAdmin := fakeVerifier{token: &auth.Token{UID: "u", Claims: map[string]interface{}{
		auth.AUTH_TENANT_MEMBERSHIPS: membership("ADMIN"),
	}}}
	plainUser := fakeVerifier{token: &auth.Token{UID: "u", Claims: map[string]interface{}{
		auth.AUTH_TENANT_MEMBERSHIPS: membership("USER"),
	}}}
	superAdmin := fakeVerifier{token: &auth.Token{UID: "u", Claims: map[string]interface{}{
		"global_roles": []interface{}{"SUPER_ADMIN"},
	}}}
	refused := fakeVerifier{err: &auth.AuthError{Code: "session_inactive", Status: 401}}
	down := fakeVerifier{err: auth.ConvertKratosError(errors.New("dial tcp: connection refused"))}
	failing := fakeVerifier{err: &auth.AuthError{Code: "kratos-error", Status: 502}}

	cases := []struct {
		name   string
		v      SessionVerifier
		site   string
		cookie bool
		want   int
	}{
		{"customer admin opens customer-admin", customerAdmin, "customer-admin", true, http.StatusNoContent},
		{"customer admin is refused super-admin", customerAdmin, "super-admin", true, http.StatusForbidden},
		{"global ADMIN opens customer-admin", globalAdmin, "customer-admin", true, http.StatusNoContent},
		{"global ADMIN is refused super-admin", globalAdmin, "super-admin", true, http.StatusForbidden},
		{"ADMIN inside a membership is ignored, as the auth provider does", membershipAdmin, "customer-admin", true, http.StatusForbidden},
		{"a user opens user", plainUser, "user", true, http.StatusNoContent},
		{"a user opens agents", plainUser, "agents", true, http.StatusNoContent},
		{"a user is refused customer-admin", plainUser, "customer-admin", true, http.StatusForbidden},
		{"super admin opens customer-admin", superAdmin, "customer-admin", true, http.StatusNoContent},
		{"super admin opens super-admin", superAdmin, "super-admin", true, http.StatusNoContent},
		{"no cookie", customerAdmin, "user", false, http.StatusUnauthorized},
		{"inactive session", refused, "user", true, http.StatusUnauthorized},
		{"kratos unreachable is 503, not 401", down, "user", true, http.StatusServiceUnavailable},
		{"kratos 5xx is 503", failing, "user", true, http.StatusServiceUnavailable},
		{"unknown site", customerAdmin, "internal", true, http.StatusBadRequest},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := askDocs(t, tc.v, tc.site, tc.cookie).Code; got != tc.want {
				t.Fatalf("status = %d, want %d", got, tc.want)
			}
		})
	}
}

func TestDocsAccessForbiddenNamesTheRole(t *testing.T) {
	v := fakeVerifier{token: &auth.Token{UID: "u", Claims: map[string]interface{}{
		auth.AUTH_TENANT_MEMBERSHIPS: membership("CUSTOMER_ADMIN"),
	}}}
	w := askDocs(t, v, "super-admin", true)
	if w.Body.String() != `{"code":"docs_role_required","role":"SUPER_ADMIN"}` {
		t.Fatalf("body = %s", w.Body.String())
	}
}
