package config

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/cto-up/core-lib/pkg/shared/auth"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

func callAs(t *testing.T, claims map[string]interface{}, method string, call func(h *TenantConfigHandler, c *gin.Context)) *httptest.ResponseRecorder {
	t.Helper()
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(method, "/api/v1/configs/tenant-configs", strings.NewReader(`{"name":"lms.authoring.llm","value":"x"}`))
	c.Request.Header.Set("Content-Type", "application/json")
	c.Set(auth.AUTH_TENANT_ID_KEY, "t1")
	c.Set(auth.AUTH_USER_ID, "u1")
	if claims != nil {
		c.Set(auth.AUTH_CLAIMS, claims)
	}
	// A nil store: a refused call must answer before it reaches the database.
	call(&TenantConfigHandler{}, c)
	return w
}

func TestTenantConfigWritesRefuseNonAdmins(t *testing.T) {
	id := uuid.New()
	writes := map[string]struct {
		method string
		call   func(h *TenantConfigHandler, c *gin.Context)
	}{
		"add":    {http.MethodPost, func(h *TenantConfigHandler, c *gin.Context) { h.AddTenantConfig(c) }},
		"update": {http.MethodPut, func(h *TenantConfigHandler, c *gin.Context) { h.UpdateTenantConfig(c, id) }},
		"delete": {http.MethodDelete, func(h *TenantConfigHandler, c *gin.Context) { h.DeleteTenantConfig(c, id) }},
	}
	callers := map[string]map[string]interface{}{
		"no claims":                   nil,
		"plain user":                  {"USER": true},
		"api token of a non-admin":    {},
		"reseller tenant, not acting": {auth.TENANT_IS_RESELLER: true},
	}
	for wname, wr := range writes {
		for cname, claims := range callers {
			t.Run(wname+"/"+cname, func(t *testing.T) {
				w := callAs(t, claims, wr.method, wr.call)
				if w.Code != http.StatusForbidden {
					t.Fatalf("status = %d, want 403 (body %s)", w.Code, w.Body.String())
				}
			})
		}
	}
}

func TestCanEditTenantSettingsAllowsWhoeverRunsTheTenant(t *testing.T) {
	for _, role := range []string{"SUPER_ADMIN", "ADMIN", "CUSTOMER_ADMIN", "ACTING_RESELLER"} {
		t.Run(role, func(t *testing.T) {
			gin.SetMode(gin.TestMode)
			c, _ := gin.CreateTestContext(httptest.NewRecorder())
			c.Set(auth.AUTH_CLAIMS, map[string]interface{}{role: true})
			if !canEditTenantSettings(c) {
				t.Fatalf("%s refused", role)
			}
		})
	}
}
