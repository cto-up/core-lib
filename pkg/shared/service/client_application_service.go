package service

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"strings"
	"time"

	"github.com/cto-up/core-lib/pkg/core/db"
	"github.com/cto-up/core-lib/pkg/core/db/repository"
	"github.com/cto-up/core-lib/pkg/shared/access"
	"github.com/cto-up/core-lib/pkg/shared/activity"
	"github.com/cto-up/core-lib/pkg/shared/util"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

// Client application service constants
const (
	// Token format: prefix_random_base62
	TokenPrefix        = "api_"
	TokenRandomLength  = 32
	TokenPrefixLength  = 8   // First 8 chars for display/identification
	DefaultTokenExpiry = 90  // 90 days default expiry
	MaxTokenExpiry     = 365 // Maximum token expiry in days
	TokenAuditCreated  = "CREATED"
	TokenAuditUsed     = "USED"
	TokenAuditRevoked  = "REVOKED"
	TokenAuditUpdated  = "UPDATED"
)

// ClientApplicationService handles client applications and API tokens
type ClientApplicationService struct {
	store *db.Store
}

// NewClientApplicationService creates a new client application service
func NewClientApplicationService(store *db.Store) *ClientApplicationService {
	return &ClientApplicationService{
		store: store,
	}
}

// CreateClientApplication creates a new client application
func (s *ClientApplicationService) CreateClientApplication(ctx context.Context, tenantID string, name, description, createdBy string) (repository.CoreClientApplication, error) {
	logger := util.GetLoggerFromCtx(ctx)
	// Tenant ID can be null for super admin (global) applications
	var tenantIDParam *string
	if tenantID != "" {
		tenantIDParam = &tenantID
	}

	app, err := s.store.CreateClientApplication(ctx, repository.CreateClientApplicationParams{
		Name:        name,
		Description: pgtype.Text{String: description, Valid: true},
		TenantID:    util.ToNullableText(tenantIDParam),
		CreatedBy:   createdBy,
	})

	if err != nil {
		logger.Err(err).Str("tenantID", tenantID).Str("name", name).Msg("Failed to create client application")
		return repository.CoreClientApplication{}, err
	}

	return app, nil
}

// GetClientApplicationByID retrieves a client application by ID
func (s *ClientApplicationService) GetClientApplicationByID(ctx context.Context, id uuid.UUID, tenantID string) (repository.CoreClientApplication, error) {
	logger := util.GetLoggerFromCtx(ctx)
	var tenantIDParam *string
	if tenantID != "" {
		tenantIDParam = &tenantID
	}

	app, err := s.store.GetClientApplicationByID(ctx, repository.GetClientApplicationByIDParams{
		ID:       id,
		TenantID: util.ToNullableText(tenantIDParam),
	})

	if err != nil {
		logger.Err(err).Str("id", id.String()).Msg("Failed to get client application")
		return repository.CoreClientApplication{}, err
	}

	return app, nil
}

// ListClientApplications returns a list of client applications
func (s *ClientApplicationService) ListClientApplications(ctx context.Context, tenantID string,
	limit, offset int32, sortBy, order string,
	searchQuery string, includeInactive bool) ([]repository.CoreClientApplication, error) {
	logger := util.GetLoggerFromCtx(ctx)
	var tenantIDParam *string
	if tenantID != "" {
		tenantIDParam = &tenantID
	}

	var includeInactiveParam *bool
	if includeInactive {
		includeInactiveParam = &includeInactive
	}

	var likeParam *pgtype.Text
	if searchQuery != "" {
		likeParam = &pgtype.Text{
			String: searchQuery + "%",
			Valid:  true,
		}
	}

	apps, err := s.store.ListClientApplications(ctx, repository.ListClientApplicationsParams{
		TenantID:        util.ToNullableText(tenantIDParam),
		IncludeInactive: util.ToNullableBool(includeInactiveParam),
		Like:            likeParam,
		Limit:           limit,
		Offset:          offset,
		SortBy:          sortBy,
		Order:           order,
	})

	if err != nil {
		logger.Err(err).Str("tenantID", tenantID).Msg("Failed to list client applications")
		return nil, err
	}

	return apps, nil
}

// UpdateClientApplication updates a client application
func (s *ClientApplicationService) UpdateClientApplication(ctx context.Context, id uuid.UUID,
	tenantID string, name, description string, active bool) (repository.CoreClientApplication, error) {

	logger := util.GetLoggerFromCtx(ctx)

	var tenantIDParam *string
	if tenantID != "" {
		tenantIDParam = &tenantID
	}

	app, err := s.store.UpdateClientApplication(ctx, repository.UpdateClientApplicationParams{
		ID:          id,
		Name:        name,
		Description: pgtype.Text{String: description, Valid: true},
		Active:      active,
		TenantID:    util.ToNullableText(tenantIDParam),
	})

	if err != nil {
		logger.Err(err).Str("id", id.String()).Msg("Failed to update client application")
		return repository.CoreClientApplication{}, err
	}

	return app, nil
}

// DeactivateClientApplication deactivates a client application
func (s *ClientApplicationService) DeactivateClientApplication(ctx context.Context, id uuid.UUID, tenantID string) error {

	logger := util.GetLoggerFromCtx(ctx)

	var tenantIDParam *string
	if tenantID != "" {
		tenantIDParam = &tenantID
	}

	_, err := s.store.DeactivateClientApplication(ctx, repository.DeactivateClientApplicationParams{
		ID:       id,
		TenantID: util.ToNullableText(tenantIDParam),
	})

	if err != nil {
		logger.Err(err).Str("id", id.String()).Msg("Failed to deactivate client application")
		return err
	}

	return nil
}

// DeleteClientApplication deletes a client application
func (s *ClientApplicationService) DeleteClientApplication(ctx context.Context, id uuid.UUID, tenantID string) error {

	logger := util.GetLoggerFromCtx(ctx)

	var tenantIDParam *string
	if tenantID != "" {
		tenantIDParam = &tenantID
	}

	_, err := s.store.DeleteClientApplication(ctx, repository.DeleteClientApplicationParams{
		ID:       id,
		TenantID: util.ToNullableText(tenantIDParam),
	})

	if err != nil {
		logger.Err(err).Str("id", id.String()).Msg("Failed to delete client application")
		return err
	}

	return nil
}

// GenerateSecureToken generates a secure random token
func (s *ClientApplicationService) GenerateSecureToken() (string, string, []byte, error) {
	// Generate random bytes
	randomBytes := make([]byte, TokenRandomLength)
	_, err := rand.Read(randomBytes)
	if err != nil {
		return "", "", nil, err
	}

	// Encode to base64
	randomString := base64.RawURLEncoding.EncodeToString(randomBytes)

	// Create full token
	token := fmt.Sprintf("%s%s", TokenPrefix, randomString)

	// Get first 8 chars of token for display purposes
	tokenPrefix := token[:TokenPrefixLength]

	// Hash token for storage
	hash := sha256.Sum256([]byte(token))
	tokenHash := hash[:]

	return token, tokenPrefix, tokenHash, nil
}

// CreateAPIToken creates a new API token for a client application
func (s *ClientApplicationService) CreateAPIToken(ctx *gin.Context, clientApplicationID uuid.UUID,
	tenantID string, name, description string, expiresInDays int, createdBy string, scopes []string) (string, repository.CoreApiToken, error) {

	logger := util.GetLoggerFromCtx(ctx)

	var tenantIDParam *string
	if tenantID != "" {
		tenantIDParam = &tenantID
	}

	// Validate the client application exists, is active, and belongs to the
	// caller's scope (tenant-specific if tenantID is set, global otherwise).
	app, err := s.store.GetClientApplicationByID(ctx, repository.GetClientApplicationByIDParams{
		ID:       clientApplicationID,
		TenantID: util.ToNullableText(tenantIDParam),
	})

	if err != nil {
		logger.Err(err).Str("clientApplicationID", clientApplicationID.String()).Msg("Failed to get client application for token creation")
		return "", repository.CoreApiToken{}, err
	}

	if !app.Active {
		logger.Error().Str("clientApplicationID", clientApplicationID.String()).Msg("Cannot create token for inactive application")
		return "", repository.CoreApiToken{}, fmt.Errorf("cannot create token for inactive application")
	}

	// Generate token
	token, tokenPrefix, tokenHash, err := s.GenerateSecureToken()
	if err != nil {
		logger.Err(err).Msg("Failed to generate secure token")
		return "", repository.CoreApiToken{}, err
	}

	// Set expiry
	if expiresInDays <= 0 {
		expiresInDays = DefaultTokenExpiry
	} else if expiresInDays > MaxTokenExpiry {
		expiresInDays = MaxTokenExpiry
	}

	expiryTime := time.Now().AddDate(0, 0, expiresInDays)

	// Create token in database
	var scopesArray []string
	if len(scopes) > 0 {
		scopesArray = scopes
	}

	// The token and its "created" row commit together (office#200).
	var apiToken repository.CoreApiToken
	err = s.inTx(ctx, func(q *repository.Queries, tx pgx.Tx) error {
		created, err := q.CreateAPIToken(ctx, repository.CreateAPITokenParams{
			ClientApplicationID: clientApplicationID,
			Name:                name,
			Description:         pgtype.Text{String: description, Valid: true},
			TokenHash:           tokenHash,
			TokenPrefix:         tokenPrefix,
			ExpiresAt:           expiryTime,
			CreatedBy:           createdBy,
			Scopes:              scopesArray,
		})
		if err != nil {
			return err
		}
		apiToken = created
		return recordToken(ctx.Request.Context(), tx, tenantID, TokenAuditCreated, created.ID, clientApplicationID, ctx.ClientIP(), ctx.GetHeader("User-Agent"))
	})
	if err != nil {
		logger.Err(err).Str("clientApplicationID", clientApplicationID.String()).Msg("Failed to create API token")
		return "", repository.CoreApiToken{}, err
	}

	// Update the client application's last used timestamp
	err = s.store.UpdateClientApplicationLastUsed(ctx, clientApplicationID)
	if err != nil {
		logger.Warn().Err(err).Str("clientApplicationID", clientApplicationID.String()).Msg("Failed to update client application last used timestamp")
	}

	return token, apiToken, nil
}

// GetAPITokenByID retrieves an API token by ID
func (s *ClientApplicationService) GetAPITokenByID(ctx context.Context, id uuid.UUID, tenantID string) (repository.GetAPITokenByIDRow, error) {
	logger := util.GetLoggerFromCtx(ctx)
	var tenantIDParam *string
	if tenantID != "" {
		tenantIDParam = &tenantID
	}

	token, err := s.store.GetAPITokenByID(ctx, repository.GetAPITokenByIDParams{
		ID:       id,
		TenantID: util.ToNullableText(tenantIDParam),
	})

	if err != nil {
		logger.Err(err).Str("id", id.String()).Msg("Failed to get API token")
		return repository.GetAPITokenByIDRow{}, err
	}

	return token, nil
}

// ListAPITokens lists API tokens for a client application
func (s *ClientApplicationService) ListAPITokens(ctx context.Context, clientApplicationID *uuid.UUID,
	tenantID string, limit, offset int32, sortBy, order string,
	includeRevoked, includeExpired bool) ([]repository.ListAPITokensRow, error) {

	logger := util.GetLoggerFromCtx(ctx)

	var tenantIDParam *string
	if tenantID != "" {
		tenantIDParam = &tenantID
	}

	var clientAppIDParam *uuid.UUID
	if clientApplicationID != nil {
		clientAppIDParam = clientApplicationID
	}

	var includeRevokedParam *bool
	if includeRevoked {
		includeRevokedParam = &includeRevoked
	}

	var includeExpiredParam *bool
	if includeExpired {
		includeExpiredParam = &includeExpired
	}

	tokens, err := s.store.ListAPITokens(ctx, repository.ListAPITokensParams{
		ClientApplicationID: util.ToNullableUUID(clientAppIDParam),
		TenantID:            util.ToNullableText(tenantIDParam),
		IncludeRevoked:      util.ToNullableBool(includeRevokedParam),
		IncludeExpired:      util.ToNullableBool(includeExpiredParam),
		Limit:               limit,
		Offset:              offset,
		SortBy:              sortBy,
		Order:               order,
	})

	if err != nil {
		logger.Err(err).Msg("Failed to list API tokens")
		return nil, err
	}

	return tokens, nil
}

// RevokeAPIToken revokes an API token
func (s *ClientApplicationService) RevokeAPIToken(ctx *gin.Context, id uuid.UUID, tenantID, reason, revokedBy string) (repository.CoreApiToken, error) {
	logger := util.GetLoggerFromCtx(ctx)

	var tenantIDParam *string
	if tenantID != "" {
		tenantIDParam = &tenantID
	}

	// Check the token exists, is not already revoked, and is in the caller's
	// scope (tenant-specific if tenantID is set, global otherwise).
	token, err := s.store.GetAPITokenByID(ctx, repository.GetAPITokenByIDParams{
		ID:       id,
		TenantID: util.ToNullableText(tenantIDParam),
	})

	if err != nil {
		logger.Err(err).Str("id", id.String()).Msg("Failed to get API token for revocation")
		return repository.CoreApiToken{}, err
	}

	if token.Revoked {
		logger.Warn().Str("id", id.String()).Msg("Token is already revoked")
		return repository.CoreApiToken{}, fmt.Errorf("token is already revoked")
	}

	// The revocation and its row commit together (office#200).
	var revokedToken repository.CoreApiToken
	err = s.inTx(ctx, func(q *repository.Queries, tx pgx.Tx) error {
		revoked, err := q.RevokeAPIToken(ctx, repository.RevokeAPITokenParams{
			ID:            id,
			RevokedReason: pgtype.Text{String: reason, Valid: true},
			RevokedBy:     pgtype.Text{String: revokedBy, Valid: true},
		})
		if err != nil {
			return err
		}
		revokedToken = revoked
		return recordToken(ctx.Request.Context(), tx, tenantID, TokenAuditRevoked, id, revoked.ClientApplicationID, ctx.ClientIP(), ctx.GetHeader("User-Agent"))
	})
	if err != nil {
		logger.Err(err).Str("id", id.String()).Msg("Failed to revoke API token")
		return repository.CoreApiToken{}, err
	}

	return revokedToken, nil
}

// DeleteAPIToken deletes an API token
func (s *ClientApplicationService) DeleteAPIToken(ctx context.Context, id uuid.UUID) error {
	logger := util.GetLoggerFromCtx(ctx)
	_, err := s.store.DeleteAPIToken(ctx, id)

	if err != nil {
		logger.Err(err).Str("id", id.String()).Msg("Failed to delete API token")
		return err
	}

	return nil
}

// GetAPITokenAuditLogs is a token's created, used and revoked rows from the
// activity log (office#200), newest first; tenantID "" is a global application.
func (s *ClientApplicationService) GetAPITokenAuditLogs(ctx context.Context, tenantID string, tokenID uuid.UUID, limit, offset int32) ([]activity.Entry, error) {
	logs, err := activity.ListTenant(ctx, s.store.ConnPool, tenantID, activity.Query{
		Target: &activity.Ref{Kind: KindAPIToken, ID: tokenID.String()}, Limit: int(limit), Offset: int(offset),
	})
	if err != nil {
		logger := util.GetLoggerFromCtx(ctx)
		logger.Err(err).Str("tokenID", tokenID.String()).Msg("Failed to get API token audit logs")
		return nil, err
	}
	return logs, nil
}

// VerifyAPIToken verifies an API token and returns the associated application and token if valid
func (s *ClientApplicationService) VerifyAPIToken(ctx *gin.Context, tokenString string) (repository.GetAPITokenByHashRow, error) {
	logger := util.GetLoggerFromCtx(ctx)
	// Sanitize token
	tokenString = strings.TrimSpace(tokenString)

	// Hash token for lookup
	hash := sha256.Sum256([]byte(tokenString))
	tokenHash := hash[:]

	// Look up token by hash
	token, err := s.store.GetAPITokenByHash(ctx, tokenHash)
	if err != nil {
		logger.Err(err).Msg("Failed to verify API token")
		return repository.GetAPITokenByHashRow{}, err
	}

	// Each use is a row, as the token itself (no Principal exists yet: this
	// runs inside the door). A failed row is logged; it never refuses the
	// token, or an audit outage would lock every integration out.
	used := access.WithPrincipal(ctx.Request.Context(), access.Principal{
		TenantID: token.TenantID.String, UserID: token.CreatedBy, Door: access.DoorAPIKey, KeyID: token.ID.String(),
	})
	if err := recordToken(used, s.store.ConnPool, token.TenantID.String, TokenAuditUsed, token.ID, token.ClientApplicationID, ctx.ClientIP(), ctx.GetHeader("User-Agent")); err != nil {
		logger.Err(err).Str("tokenID", token.ID.String()).Msg("Failed to record the API token's use")
	}
	ipAddress := ctx.ClientIP()

	// Update token last used
	err = s.store.UpdateAPITokenLastUsed(ctx, repository.UpdateAPITokenLastUsedParams{
		ID:        token.ID,
		IpAddress: pgtype.Text{String: ipAddress, Valid: true},
	})

	if err != nil {
		logger.Err(err).Str("tokenID", token.ID.String()).Msg("Failed to update token last used timestamp")
	}

	// Update the client application's last used timestamp
	err = s.store.UpdateClientApplicationLastUsed(ctx, token.ClientApplicationID)
	if err != nil {
		logger.Err(err).Str("clientApplicationID", token.ClientApplicationID.String()).Msg("Failed to update client application last used timestamp")
	}

	return token, nil
}
