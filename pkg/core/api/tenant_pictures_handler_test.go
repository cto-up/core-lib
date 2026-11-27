package core

import (
	"bytes"
	"context"
	"encoding/json"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	_ "gocloud.dev/blob/memblob"

	api "github.com/cto-up/core-lib/api/openapi/core"
	"github.com/cto-up/core-lib/pkg/shared/auth"
	"github.com/cto-up/core-lib/pkg/shared/config"
	fileservice "github.com/cto-up/core-lib/pkg/shared/fileservice"
)

// pictureHandler is a TenantHandler whose file service is an in-memory bucket.
func pictureHandler(t *testing.T) *TenantHandler {
	t.Helper()
	gin.SetMode(gin.TestMode)
	prev := config.Current()
	cfg := prev
	cfg.Storage = config.Storage{LocalPath: "mem://"}
	config.Set(cfg)
	t.Cleanup(func() { config.Set(prev) })
	return &TenantHandler{FileService: fileservice.NewFileService()}
}

func webpBytes(size int) []byte {
	b := make([]byte, size)
	copy(b, "RIFF\x00\x00\x00\x00WEBPVP8 ")
	return b
}

type pictureCall struct {
	tenantID string // "" leaves the tenant off the context
	admin    bool
	field    string
	filename string
	body     []byte
}

func (pc pictureCall) run(h *TenantHandler, upload func(*TenantHandler, *gin.Context)) *httptest.ResponseRecorder {
	var form bytes.Buffer
	mw := multipart.NewWriter(&form)
	part, _ := mw.CreateFormFile(pc.field, pc.filename)
	_, _ = part.Write(pc.body)
	_ = mw.Close()

	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/api/v1/tenant/pictures/logo", &form)
	c.Request.Header.Set("Content-Type", mw.FormDataContentType())
	if pc.tenantID != "" {
		c.Set(auth.AUTH_TENANT_ID_KEY, pc.tenantID)
	}
	c.Set(auth.AUTH_CLAIMS, map[string]interface{}{string(api.ADMIN): pc.admin})
	upload(h, c)
	c.Writer.WriteHeaderNow()
	return rec
}

func messageOf(t *testing.T, rec *httptest.ResponseRecorder) string {
	t.Helper()
	var body struct{ Message string }
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("response %q is not a JSON message: %v", rec.Body.String(), err)
	}
	return body.Message
}

func TestUploadTenantPicture(t *testing.T) {
	png := append([]byte("\x89PNG\r\n\x1a\n"), make([]byte, 64)...)
	tests := []struct {
		name    string
		call    pictureCall
		status  int
		message string
	}{
		{"admin stores a WebP", pictureCall{"t1", true, "picture", "logo.webp", webpBytes(2048)}, http.StatusNoContent, ""},
		{"WebP bytes under another name are accepted", pictureCall{"t1", true, "picture", "logo.png", webpBytes(2048)}, http.StatusNoContent, ""},
		{"PNG renamed .webp is refused", pictureCall{"t1", true, "picture", "logo.webp", png}, http.StatusBadRequest, "only WebP images are accepted"},
		{"truncated RIFF header is refused", pictureCall{"t1", true, "picture", "logo.webp", []byte("RIFF\x00\x00")}, http.StatusBadRequest, "only WebP images are accepted"},
		{"wrong form field", pictureCall{"t1", true, "file", "logo.webp", webpBytes(2048)}, http.StatusBadRequest, `the form has no "picture" file`},
		{"6 MB is too large", pictureCall{"t1", true, "picture", "logo.webp", webpBytes(6 << 20)}, http.StatusRequestEntityTooLarge, "the picture exceeds 5 MB"},
		{"just over 5 MB is too large", pictureCall{"t1", true, "picture", "logo.webp", webpBytes(maxTenantPictureBytes + 1)}, http.StatusRequestEntityTooLarge, "the picture exceeds 5 MB"},
		{"non-admin is forbidden", pictureCall{"t1", false, "picture", "logo.webp", webpBytes(2048)}, http.StatusForbidden, ""},
		{"missing tenant is a 400, not a panic", pictureCall{"", true, "picture", "logo.webp", webpBytes(2048)}, http.StatusBadRequest, "the request names no tenant"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := pictureHandler(t)
			rec := tt.call.run(h, (*TenantHandler).UploadTenantLogo)
			if rec.Code != tt.status {
				t.Fatalf("status = %d, want %d (body %s)", rec.Code, tt.status, rec.Body.String())
			}
			if tt.message != "" {
				if got := messageOf(t, rec); got != tt.message {
					t.Errorf("message = %q, want %q", got, tt.message)
				}
			}
			stored, _ := h.FileService.FileExists(context.Background(), pictureLogo.path("t1"))
			if stored != (tt.status == http.StatusNoContent) {
				t.Errorf("picture stored = %v after status %d", stored, rec.Code)
			}
		})
	}
}

func TestExactlyFiveMegabytesIsAccepted(t *testing.T) {
	h := pictureHandler(t)
	rec := pictureCall{"t1", true, "picture", "logo.webp", webpBytes(maxTenantPictureBytes)}.run(h, (*TenantHandler).UploadTenantLogo)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204 (body %s)", rec.Code, rec.Body.String())
	}
}

// The storage keys are what existing tenants' pictures are saved under; a
// change here would make every stored picture vanish.
func TestEachPictureKeepsItsStoragePath(t *testing.T) {
	tests := []struct {
		upload func(*TenantHandler, *gin.Context)
		serve  func(*TenantHandler, *gin.Context)
		path   string
	}{
		{(*TenantHandler).UploadTenantLogo,
			func(h *TenantHandler, c *gin.Context) { h.GetTenantLogo(c, api.GetTenantLogoParams{}) },
			"/tenants/t1/core/pictures/logo.webp"},
		{(*TenantHandler).UploadTenantBackground,
			func(h *TenantHandler, c *gin.Context) { h.GetTenantBackground(c, api.GetTenantBackgroundParams{}) },
			"/tenants/t1/core/pictures/bg.webp"},
		{(*TenantHandler).UploadTenantBackgroundMobile,
			func(h *TenantHandler, c *gin.Context) {
				h.GetTenantBackgroundMobile(c, api.GetTenantBackgroundMobileParams{})
			},
			"/tenants/t1/core/pictures/bg-mobile.webp"},
	}
	for _, tt := range tests {
		t.Run(tt.path, func(t *testing.T) {
			h := pictureHandler(t)
			picture := webpBytes(4096)
			if rec := (pictureCall{"t1", true, "picture", "x.webp", picture}).run(h, tt.upload); rec.Code != http.StatusNoContent {
				t.Fatalf("upload status = %d (body %s)", rec.Code, rec.Body.String())
			}
			if ok, _ := h.FileService.FileExists(context.Background(), tt.path); !ok {
				t.Fatalf("nothing stored at %s", tt.path)
			}

			rec := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(rec)
			c.Request = httptest.NewRequest(http.MethodGet, "/public-api/v1/tenant/pictures", nil)
			c.Set(auth.AUTH_TENANT_ID_KEY, "t1")
			tt.serve(h, c)
			c.Writer.WriteHeaderNow()
			if rec.Code != http.StatusOK || !bytes.Equal(rec.Body.Bytes(), picture) {
				t.Fatalf("GET status = %d, %d bytes; want 200 with the uploaded %d bytes", rec.Code, rec.Body.Len(), len(picture))
			}
			if rec.Header().Get("ETag") == "" || rec.Header().Get("Content-Type") != "image/webp" {
				t.Errorf("GET headers = %v", rec.Header())
			}
		})
	}
}

func TestGetTenantPictureWithoutTenantIs404(t *testing.T) {
	for _, tenant := range []any{nil, ""} {
		h := pictureHandler(t)
		rec := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(rec)
		c.Request = httptest.NewRequest(http.MethodGet, "/public-api/v1/tenant/pictures/logo", nil)
		if tenant != nil {
			c.Set(auth.AUTH_TENANT_ID_KEY, tenant)
		}
		h.GetTenantLogo(c, api.GetTenantLogoParams{})
		c.Writer.WriteHeaderNow()
		if rec.Code != http.StatusNotFound {
			t.Errorf("tenant %#v: status = %d, want 404", tenant, rec.Code)
		}
		if got := messageOf(t, rec); got != "the request names no tenant" {
			t.Errorf("tenant %#v: message = %q", tenant, got)
		}
	}
}
