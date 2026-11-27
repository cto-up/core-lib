package service

import (
	"errors"
	"net/http"

	"github.com/cto-up/core-lib/pkg/shared/auth"
	utils "github.com/cto-up/core-lib/pkg/shared/util"
	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5"
	"github.com/rs/zerolog/log"
)

// TenantMiddleware is middleware to extract tenant information from the request and set it in the context
type TenantMiddleware struct {
	multitenantService *MultitenantService
}

// New is constructor of the middleware
func NewTenantMiddleware(unAuthorized func(c *gin.Context), multitenantService *MultitenantService) *TenantMiddleware {
	return &TenantMiddleware{
		multitenantService: multitenantService,
	}
}

// MiddlewareFunc resolves the request's tenant from its subdomain and puts it
// on the context; see resolveTenant.
func (fam *TenantMiddleware) MiddlewareFunc() gin.HandlerFunc {
	return fam.resolveTenant
}

func (fam *TenantMiddleware) resolveTenant(ctx *gin.Context) {
	subdomain, err := utils.GetSubdomain(ctx)
	if err != nil {
		ctx.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	// Infrastructure subdomains have no tenant — let the request through
	// with no tenant context.
	//
	// "api" belongs here for the same reason as "auth": it names a vhost,
	// never a tenant. It matters only for requests that arrive WITHOUT an
	// Origin header, because GetHost prefers Origin and falls back to Host —
	// so every normal browser call still resolves its tenant from Origin and
	// is unaffected by this branch.
	//
	// Requests with no Origin are exactly the ones an external system makes:
	// a provider redirecting a browser to an OAuth callback (a cross-site
	// top-level navigation sends no Origin), or a webhook POST. Those used
	// to resolve subdomain "api", find no tenant, and get 404 "Tenant not
	// found" before reaching the handler — which in both cases carries its
	// own tenant context anyway (the OAuth `state` row, the webhook id).
	if IsInfrastructureSubdomain(subdomain) {
		ctx.Set(auth.AUTH_TENANT_ID_KEY, "")
		return
	}

	// One DB call per (subdomain, cache TTL): GetTenantBySubdomainCached
	// loads the full tenant and warms both the subdomain → tenant_id map
	// and the tenant-record cache. Downstream middleware and handlers read
	// flags (IsDisabled, IsReseller, AllowSignUp, …) off the gin.Context.
	tenant, err := fam.multitenantService.GetTenantBySubdomainCached(ctx, subdomain)
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		log.Info().Str("subdomain", subdomain).Msg("Tenant not found")
		abortWithStatus(ctx, http.StatusNotFound, "Tenant not found")
		return
	case err != nil:
		log.Err(err).Msg("Failed to load tenant by subdomain")
		abortWithStatus(ctx, http.StatusInternalServerError, err.Error())
		return
	case tenant.IsDisabled:
		abortWithStatus(ctx, http.StatusForbidden, "Tenant account has been suspended")
		return
	}

	ctx.Set(auth.AUTH_TENANT, tenant)
	ctx.Set(auth.AUTH_TENANT_ID_KEY, tenant.TenantID)
	ctx.Next()
}

// abortWithStatus answers with the {status, message} body the tenant
// middleware has always used.
func abortWithStatus(ctx *gin.Context, status int, message string) {
	ctx.AbortWithStatusJSON(status, gin.H{"status": status, "message": message})
}

// IsInfrastructureSubdomain reports whether a subdomain names a vhost rather
// than a tenant: the root site, Kratos (auth), the api, and the docs sites
// (lms ADR 062).
func IsInfrastructureSubdomain(subdomain string) bool {
	return utils.IsAdminSubdomain(subdomain) ||
		subdomain == "auth" || subdomain == "api" || subdomain == "docs"
}
