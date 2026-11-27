package core

import (
	"bytes"
	"errors"
	"io"
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/cto-up/core-lib/api/helpers"
	api "github.com/cto-up/core-lib/api/openapi/core"
	"github.com/cto-up/core-lib/pkg/shared/auth"
	"github.com/cto-up/core-lib/pkg/shared/util"
)

// maxTenantPictureBytes caps one uploaded tenant picture.
const maxTenantPictureBytes = 5 << 20

// multipartSlack covers the multipart boundaries and part headers around the
// picture, so a picture just under the cap is not refused for its envelope.
const multipartSlack = 64 << 10

// tenantPicture names the stored object: its value is the file's base name,
// kept as it has always been so existing pictures keep loading.
type tenantPicture string

const (
	pictureLogo             tenantPicture = "logo"
	pictureBackground       tenantPicture = "bg"
	pictureBackgroundMobile tenantPicture = "bg-mobile"
)

func (p tenantPicture) path(tenantID string) string {
	return "/tenants/" + tenantID + "/core/pictures/" + string(p) + ".webp"
}

var (
	errPictureNotWebP  = errors.New("only WebP images are accepted")
	errPictureTooLarge = errors.New("the picture exceeds 5 MB")
	errPictureMissing  = errors.New(`the form has no "picture" file`)
	errNoTenant        = errors.New("the request names no tenant")
)

// (POST /api/v1/tenant/pictures/logo)
func (s *TenantHandler) UploadTenantLogo(c *gin.Context) {
	s.storeTenantPicture(c, pictureLogo)
}

// (POST /api/v1/tenant/pictures/background)
func (s *TenantHandler) UploadTenantBackground(c *gin.Context) {
	s.storeTenantPicture(c, pictureBackground)
}

// (POST /api/v1/tenant/pictures/background-mobile)
func (s *TenantHandler) UploadTenantBackgroundMobile(c *gin.Context) {
	s.storeTenantPicture(c, pictureBackgroundMobile)
}

// (GET /public-api/v1/tenant/pictures/logo)
func (s *TenantHandler) GetTenantLogo(c *gin.Context, _ api.GetTenantLogoParams) {
	s.serveTenantPicture(c, pictureLogo)
}

// (GET /public-api/v1/tenant/pictures/background)
func (s *TenantHandler) GetTenantBackground(c *gin.Context, _ api.GetTenantBackgroundParams) {
	s.serveTenantPicture(c, pictureBackground)
}

// (GET /public-api/v1/tenant/pictures/background-mobile)
func (s *TenantHandler) GetTenantBackgroundMobile(c *gin.Context, _ api.GetTenantBackgroundMobileParams) {
	s.serveTenantPicture(c, pictureBackgroundMobile)
}

// serveTenantPicture leaves ETag, 304 and 404 to the file service; a request
// on a host with no tenant has no picture either.
func (s *TenantHandler) serveTenantPicture(c *gin.Context, p tenantPicture) {
	tenantID := c.GetString(auth.AUTH_TENANT_ID_KEY)
	if tenantID == "" {
		c.AbortWithStatusJSON(http.StatusNotFound, helpers.ErrorStringResponse(errNoTenant.Error()))
		return
	}
	_ = s.FileService.GetFile(c, p.path(tenantID))
}

func (s *TenantHandler) storeTenantPicture(c *gin.Context, p tenantPicture) {
	logger := util.GetLoggerFromCtx(c.Request.Context())
	if !auth.HasAdminPrivileges(c) {
		c.AbortWithStatusJSON(http.StatusForbidden, helpers.ErrorStringResponse(
			"only a tenant admin, a super admin or an acting reseller can change the tenant pictures"))
		return
	}
	tenantID := c.GetString(auth.AUTH_TENANT_ID_KEY)
	if tenantID == "" {
		c.AbortWithStatusJSON(http.StatusBadRequest, helpers.ErrorStringResponse(errNoTenant.Error()))
		return
	}

	data, err := readPictureField(c)
	switch {
	case errors.Is(err, errPictureTooLarge):
		c.AbortWithStatusJSON(http.StatusRequestEntityTooLarge, helpers.ErrorStringResponse(err.Error()))
		return
	case errors.Is(err, errPictureMissing), errors.Is(err, errPictureNotWebP):
		c.AbortWithStatusJSON(http.StatusBadRequest, helpers.ErrorStringResponse(err.Error()))
		return
	case err != nil:
		logger.Err(err).Str("picture", string(p)).Msg("reading the uploaded tenant picture failed")
		c.AbortWithStatusJSON(http.StatusInternalServerError, helpers.ErrorStringResponse("the picture could not be read"))
		return
	}

	if err := s.FileService.SaveFile(c, data, p.path(tenantID)); err != nil {
		logger.Err(err).Str("picture", string(p)).Msg("storing the tenant picture failed")
		c.AbortWithStatusJSON(http.StatusInternalServerError, helpers.ErrorStringResponse("the picture could not be stored"))
		return
	}
	c.Status(http.StatusNoContent)
}

// readPictureField returns the "picture" part's bytes once they are known to
// be a WebP image within maxTenantPictureBytes. The body is capped before the
// form is parsed, so an oversized upload is cut off rather than spooled.
func readPictureField(c *gin.Context) ([]byte, error) {
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, maxTenantPictureBytes+multipartSlack)
	header, err := c.FormFile("picture")
	if err != nil {
		var tooBig *http.MaxBytesError
		if errors.As(err, &tooBig) {
			return nil, errPictureTooLarge
		}
		return nil, errPictureMissing
	}
	if header.Size > maxTenantPictureBytes {
		return nil, errPictureTooLarge
	}
	f, err := header.Open()
	if err != nil {
		return nil, err
	}
	defer f.Close()

	data, err := io.ReadAll(io.LimitReader(f, maxTenantPictureBytes+1))
	if err != nil {
		return nil, err
	}
	if len(data) > maxTenantPictureBytes {
		return nil, errPictureTooLarge
	}
	if !isWebP(data) {
		return nil, errPictureNotWebP
	}
	return data, nil
}

// isWebP checks the RIFF container header: "RIFF", a 4-byte size, "WEBP".
func isWebP(data []byte) bool {
	return len(data) >= 12 &&
		bytes.Equal(data[0:4], []byte("RIFF")) &&
		bytes.Equal(data[8:12], []byte("WEBP"))
}
