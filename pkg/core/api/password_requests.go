package core

import (
	"fmt"
	"strings"

	"github.com/cto-up/core-lib/pkg/shared/auth"
	"github.com/cto-up/core-lib/pkg/shared/mailer"
	"github.com/cto-up/core-lib/pkg/shared/util"
	"github.com/gin-gonic/gin"

	utils "github.com/cto-up/core-lib/pkg/shared/util"
)

// buildTenantURL constructs a URL for a specific tenant subdomain with proper port handling
// If subdomain is empty, uses the current subdomain from the request
func buildTenantURL(c *gin.Context, path string, subdomain string) (string, error) {
	host, err := utils.GetHost(c)
	if err != nil {
		return "", err
	}

	// If no subdomain passed, return the full host which includes existing subdomain
	if subdomain == "" {
		url := fmt.Sprintf("%s://%s%s", host.Scheme, host.Host, path)
		return url, nil
	}

	// Get base domain with port
	domain, err := utils.GetBaseDomainWithPort(c)
	if err != nil {
		return "", err
	}

	// Build URL with subdomain and domain (which includes port if present)
	url := fmt.Sprintf("%s://%s.%s%s", host.Scheme, subdomain, domain, path)
	return url, nil
}

// isAdminParticuleRequest returns true when the request originates from the backoffice
//
// Detection strategy (in priority order):
//
//	X-App-Source: admin header — explicit, reliable for cross-origin requests
//
// Note: browsers send only the origin (no path) in Referer/Origin for cross-origin requests
// (strict-origin-when-cross-origin policy), so the X-App-Source header is the reliable signal.
func isAdminParticuleRequest(c *gin.Context) bool {
	return c.GetHeader("X-App-Source") == "admin"
}

func getResetPasswordURL(c *gin.Context, subdomains ...string) (string, error) {
	var subdomain string
	if len(subdomains) > 0 {
		subdomain = subdomains[0]
	}

	path := "/signin?from=/"
	if isAdminParticuleRequest(c) {
		path = "/admin/signin?from=/"
	}

	return buildTenantURL(c, path, subdomain)
}

// getWelcomeEmailURL always returns the frontoffice signin URL regardless of request origin.
// New users are always directed to the frontoffice, even when an admin creates them.
func getWelcomeEmailURL(c *gin.Context, subdomains ...string) (string, error) {
	var subdomain string
	if len(subdomains) > 0 {
		subdomain = subdomains[0]
	}
	return buildTenantURL(c, "/signin?from=/", subdomain)
}

func resetPasswordRequest(c *gin.Context, baseAuthClient auth.AuthClient, url, toEmail string) error {
	logger := util.GetLoggerFromCtx(c.Request.Context())

	actionCodeSettings := &auth.ActionCodeSettings{
		URL: url,
	}

	link, err := baseAuthClient.PasswordResetLinkWithSettings(c, toEmail, actionCodeSettings)
	if err != nil {
		logger.Err(err).Msg("Failed to generate reset link")
		if strings.HasPrefix(err.Error(), auth.ErrorCodeUserNotFound) {
			logger.Warn().Str("email", toEmail).Msg("Password reset requested for non-existent user")
			return nil // Don't return an error to avoid revealing user existence
		}
		return err
	}

	// Log the generated link to verify it's not empty
	if link == "" {
		logger.Error().Str("email", toEmail).Str("url", url).Msg("Returned empty password reset link")
		return fmt.Errorf("Returned empty password reset link")
	}
	linkPrefix := link
	if len(link) > 10 {
		linkPrefix = link[:10]
	}
	logger.Info().Str("link_prefix", linkPrefix).Int("link_length", len(link)).Str("email", toEmail).Msg("Successfully generated password reset link")

	// Send the link via email (implement your email sending logic here)
	templateData := struct {
		Link string
	}{
		Link: link,
	}

	body, err := mailer.RenderDomainTemplate(c, "email-reset.html", templateData)
	if err != nil {
		logger.Err(err).Msg("Failed to parse template for reset link")
		return err
	}

	if err := mailer.Send(c, mailer.SystemAccount(), mailer.Message{TenantID: c.GetString(auth.AUTH_TENANT_ID_KEY), Kind: "core.password_reset", To: []string{toEmail}, Subject: "Reset Password Link", Body: body, HTML: true}); err != nil {
		logger.Err(err).Msg("Failed to send reset link")
		return err
	}
	return nil
}

func sendWelcomeEmail(c *gin.Context, baseAuthClient auth.AuthClient, url, toEmail string) error {
	logger := util.GetLoggerFromCtx(c.Request.Context())

	actionCodeSettings := &auth.ActionCodeSettings{
		URL: url,
	}

	link, err := baseAuthClient.PasswordResetLinkWithSettings(c, toEmail, actionCodeSettings)
	if err != nil {
		logger.Err(err).Msg("Failed to generate reset link")
		return err
	}

	// Send the link via email (implement your email sending logic here)
	templateData := struct {
		Link string
	}{
		Link: link,
	}

	body, err := mailer.RenderDomainTemplate(c, "email-welcome.html", templateData)
	if err != nil {
		logger.Err(err).Msg("Failed to parse template for reset link")
		return err
	}

	if err := mailer.Send(c, mailer.SystemAccount(), mailer.Message{TenantID: c.GetString(auth.AUTH_TENANT_ID_KEY), Kind: "core.password_set", To: []string{toEmail}, Subject: "Welcome, Set Your Password", Body: body, HTML: true}); err != nil {
		logger.Err(err).Msg("Failed to send reset link")
		return err
	}
	return nil

}

func getConfirmationEmailURL(c *gin.Context) (string, error) {
	logger := util.GetLoggerFromCtx(c.Request.Context())
	domainInfo, err := utils.GetDomainInfo(c)
	if err != nil {
		logger.Err(err).Msg("Failed to get domain info for confirmation email URL")
		return "", err
	}
	baseURL := domainInfo.BaseURL

	url := fmt.Sprintf("%s/verify-email", baseURL)

	return url, nil
}

func sendConfirmationEmail(c *gin.Context, url, toEmail string, confirmationToken string) error {
	logger := util.GetLoggerFromCtx(c.Request.Context())

	// Option: Generate the email verification link manually
	link := fmt.Sprintf("%s?token=%s", url, confirmationToken)

	// Send the link via email
	templateData := struct {
		Link  string
		Email string
	}{
		Link:  link,
		Email: toEmail,
	}

	body, err := mailer.RenderDomainTemplate(c, "email-verification.html", templateData)
	if err != nil {
		logger.Err(err).Msg("Failed to parse template for email verification")
		return err
	}

	if err := mailer.Send(c, mailer.SystemAccount(), mailer.Message{TenantID: c.GetString(auth.AUTH_TENANT_ID_KEY), Kind: "core.email_verification", To: []string{toEmail}, Subject: "Please verify your email address", Body: body, HTML: true}); err != nil {
		logger.Err(err).Msg("Failed to send email verification")
		return err
	}
	return nil
}

// sendAlreadyRegisteredEmail is sent when someone attempts to sign up with an
// email that already has an account on the tenant. It links to signin and
// (optionally) to a password-reset flow, without leaking which it was through
// the signup response.
func sendAlreadyRegisteredEmail(c *gin.Context, baseAuthClient auth.AuthClient, signinURL, toEmail, tenantName string) error {
	logger := util.GetLoggerFromCtx(c.Request.Context())

	resetLink, err := baseAuthClient.PasswordResetLinkWithSettings(c, toEmail, &auth.ActionCodeSettings{URL: signinURL})
	if err != nil {
		logger.Err(err).Msg("Failed to generate reset link for already-registered email")
		// Continue without reset link — user can still sign in.
		resetLink = signinURL
	}

	templateData := struct {
		Email      string
		TenantName string
		SigninLink string
		ResetLink  string
	}{
		Email:      toEmail,
		TenantName: tenantName,
		SigninLink: signinURL,
		ResetLink:  resetLink,
	}

	body, err := mailer.RenderDomainTemplate(c, "email-already-registered.html", templateData)
	if err != nil {
		logger.Err(err).Msg("Failed to parse template for already-registered email")
		return err
	}

	if err := mailer.Send(c, mailer.SystemAccount(), mailer.Message{TenantID: c.GetString(auth.AUTH_TENANT_ID_KEY), Kind: "core.signup_attempt", To: []string{toEmail}, Subject: "Sign up attempt on " + tenantName, Body: body, HTML: true}); err != nil {
		logger.Err(err).Msg("Failed to send already-registered email")
		return err
	}
	logger.Info().Str("email", toEmail).Str("tenant", tenantName).Msg("Already-registered email sent")
	return nil
}

func sendTenantAddedEmail(c *gin.Context, baseAuthClient auth.AuthClient, url, toEmail, tenantName string) error {
	logger := util.GetLoggerFromCtx(c.Request.Context())

	// Send the notification email
	templateData := struct {
		Link       string
		TenantName string
	}{
		Link:       url,
		TenantName: tenantName,
	}

	body, err := mailer.RenderDomainTemplate(c, "email-tenant-added.html", templateData)
	if err != nil {
		logger.Err(err).Msg("Failed to parse template for tenant added notification")
		return err
	}

	if err := mailer.Send(c, mailer.SystemAccount(), mailer.Message{TenantID: c.GetString(auth.AUTH_TENANT_ID_KEY), Kind: "core.membership_added", To: []string{toEmail}, Subject: "You've been added to " + tenantName, Body: body, HTML: true}); err != nil {
		logger.Err(err).Msg("Failed to send tenant added notification")
		return err
	}
	logger.Info().Str("email", toEmail).Str("tenant", tenantName).Msg("Tenant added notification sent successfully")
	return nil
}

// returnPath must already have been through safeReturnPath.
func sendMagicLink(c *gin.Context, baseAuthClient auth.AuthClient, origin, toEmail, returnPath string) error {
	logger := util.GetLoggerFromCtx(c.Request.Context())

	actionCodeSettings := &auth.ActionCodeSettings{
		URL:  origin + "/signin", // Redirect to signin after using the link
		From: returnPath,
	}

	link, err := baseAuthClient.PasswordResetLinkWithSettings(c, toEmail, actionCodeSettings)
	if err != nil {
		logger.Err(err).Msg("Failed to generate magic link (recovery link)")
		return err
	}

	templateData := struct {
		Link string
	}{
		Link: link,
	}

	body, err := mailer.RenderDomainTemplate(c, "email-magic-link.html", templateData)
	if err != nil {
		logger.Err(err).Msg("Failed to parse template for magic link")
		return err
	}

	if err := mailer.Send(c, mailer.SystemAccount(), mailer.Message{TenantID: c.GetString(auth.AUTH_TENANT_ID_KEY), Kind: "core.magic_link", To: []string{toEmail}, Subject: "Welcome! Access Your Account", Body: body, HTML: true}); err != nil {
		logger.Err(err).Msg("Failed to send magic link email")
		return err
	}
	logger.Info().Str("email", toEmail).Msg("Magic link email sent successfully")
	return nil
}
