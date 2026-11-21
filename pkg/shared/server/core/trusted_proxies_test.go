package core_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/cto-up/core-lib/pkg/shared/config"
	"github.com/cto-up/core-lib/pkg/shared/server/core"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func clientIP(t *testing.T, trusted []string, remote string, headers map[string]string) string {
	t.Helper()
	gin.SetMode(gin.TestMode)
	r := gin.New()
	require.NoError(t, core.TrustClientIPFrom(r, trusted))
	r.GET("/ip", func(c *gin.Context) { c.String(http.StatusOK, c.ClientIP()) })
	req := httptest.NewRequest(http.MethodGet, "/ip", nil)
	req.RemoteAddr = remote
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	return rec.Body.String()
}

func TestTheClientIPComesFromXRealIPOnlyWhenATrustedProxySetIt(t *testing.T) {
	spoof := "203.0.113.9"
	cases := []struct {
		name    string
		remote  string
		headers map[string]string
		want    string
	}{
		{"an untrusted peer cannot name the client", "198.51.100.7:4000",
			map[string]string{"X-Real-IP": spoof, "X-Forwarded-For": spoof}, "198.51.100.7"},
		{"nginx names the client and X-Forwarded-For cannot override it", "172.18.0.1:4000",
			map[string]string{"X-Real-IP": "1.2.3.4", "X-Forwarded-For": spoof + ", 1.2.3.4"}, "1.2.3.4"},
		{"nginx without X-Real-IP gives nginx's own address", "172.18.0.1:4000",
			map[string]string{"X-Forwarded-For": spoof}, "172.18.0.1"},
		{"nginx with an X-Real-IP that is not an address gives nginx's own address", "172.18.0.1:4000",
			map[string]string{"X-Real-IP": "not-an-ip", "X-Forwarded-For": spoof}, "172.18.0.1"},
		{"a split prod's web1 over the mesh names the client", "10.10.0.10:4000",
			map[string]string{"X-Real-IP": "1.2.3.4"}, "1.2.3.4"},
		{"the tailnet is not a proxy", "100.101.102.103:4000",
			map[string]string{"X-Real-IP": spoof}, "100.101.102.103"},
		{"local dev through the Vite proxy", "127.0.0.1:4000", nil, "127.0.0.1"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, clientIP(t, config.DefaultTrustedProxies, tc.remote, tc.headers))
		})
	}
}

func TestOnlyTheConfiguredProxiesAreTrustedWhenSet(t *testing.T) {
	got := clientIP(t, []string{"172.18.0.1"}, "10.10.0.10:4000", map[string]string{"X-Real-IP": "1.2.3.4"})
	assert.Equal(t, "10.10.0.10", got)
}
