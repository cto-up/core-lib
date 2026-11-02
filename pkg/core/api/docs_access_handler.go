package core

import (
	"context"
	"net/http"

	"github.com/cto-up/core-lib/api/openapi/core"
	"github.com/cto-up/core-lib/pkg/shared/auth"
	"github.com/gin-gonic/gin"
)

// DocsAccessPath is asked by a product's nginx (auth_request) before it serves
// a page of a role-gated docs site (lms ADR 062). It is tenant-less: product
// docs are not tenant data, so admin of any tenant opens the customer-admin site.
const DocsAccessPath = "/public-api/v1/docs/access"

// SessionVerifier is the one call the docs gate needs from the auth client.
type SessionVerifier interface {
	VerifySessionCredential(ctx context.Context, cred auth.SessionCredential) (*auth.Token, error)
}

type docsRule func(globalRoles, tenantRoles map[string]bool) bool

func anySession(_, _ map[string]bool) bool { return true }

func customerAdmin(global, tenant map[string]bool) bool {
	// ADMIN is global-only, as the auth provider reads it: an ADMIN inside a
	// tenant membership is legacy data it ignores, and so does this.
	return global[string(core.SUPERADMIN)] || global[string(core.ADMIN)] ||
		tenant[string(core.CUSTOMERADMIN)]
}

func superAdmin(global, _ map[string]bool) bool { return global[string(core.SUPERADMIN)] }

var docsSites = map[string]struct {
	rule docsRule
	role string
}{
	"user":           {anySession, string(core.USER)},
	"agents":         {anySession, string(core.USER)},
	"workflow":       {anySession, string(core.USER)},
	"customer-admin": {customerAdmin, string(core.CUSTOMERADMIN)},
	"super-admin":    {superAdmin, string(core.SUPERADMIN)},
}

type DocsAccessHandler struct {
	verifier SessionVerifier
}

func NewDocsAccessHandler(verifier SessionVerifier) *DocsAccessHandler {
	return &DocsAccessHandler{verifier: verifier}
}

// GetDocsAccess answers 204 (read it), 401 (no active session), 403 (the
// session lacks the role; the body names it) or 503 (the auth provider did not
// answer). 503 is kept apart from 401 so nginx shows an outage, not a sign-in
// page that would loop.
func (h *DocsAccessHandler) GetDocsAccess(c *gin.Context) {
	site, ok := docsSites[c.Query("site")]
	if !ok {
		c.JSON(http.StatusBadRequest, gin.H{"code": "unknown_docs_site", "site": c.Query("site")})
		return
	}
	cred, ok := auth.ExtractSessionCredential(c)
	if !ok {
		c.Status(http.StatusUnauthorized)
		return
	}
	token, err := h.verifier.VerifySessionCredential(c.Request.Context(), cred)
	if err != nil {
		if auth.IsUnavailable(err) {
			c.JSON(http.StatusServiceUnavailable, gin.H{"code": "auth_unavailable"})
			return
		}
		c.Status(http.StatusUnauthorized)
		return
	}
	global, tenant := rolesOf(token.Claims)
	if !site.rule(global, tenant) {
		c.JSON(http.StatusForbidden, gin.H{"code": "docs_role_required", "role": site.role})
		return
	}
	c.Status(http.StatusNoContent)
}

func rolesOf(claims map[string]interface{}) (global, tenant map[string]bool) {
	global, tenant = map[string]bool{}, map[string]bool{}
	if roles, ok := claims["global_roles"].([]interface{}); ok {
		for _, r := range roles {
			if s, ok := r.(string); ok {
				global[s] = true
			}
		}
	}
	if memberships, ok := claims[auth.AUTH_TENANT_MEMBERSHIPS].([]interface{}); ok {
		for _, m := range memberships {
			mm, _ := m.(map[string]interface{})
			roles, _ := mm["roles"].([]interface{})
			for _, r := range roles {
				if s, ok := r.(string); ok {
					tenant[s] = true
				}
			}
		}
	}
	return global, tenant
}

// RegisterDocsAccessRoute mounts the check with no middleware: it resolves no
// tenant and verifies the session itself, so the tenant middleware's lookup
// of the docs host would only get in the way.
func RegisterDocsAccessRoute(router gin.IRouter, h *DocsAccessHandler) {
	router.GET(DocsAccessPath, h.GetDocsAccess)
	router.HEAD(DocsAccessPath, h.GetDocsAccess)
}
