package service

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/cto-up/core-lib/pkg/core/db/repository"
	"github.com/cto-up/core-lib/pkg/shared/access"
	"github.com/cto-up/core-lib/pkg/shared/activity"
)

// KindAPIToken is the activity kind of an API token (office#200).
const KindAPIToken = "api_token"

// Activity actions of a token, for each TokenAudit* the audit endpoint shows.
var tokenActions = map[string]string{
	TokenAuditCreated: "api_token.created",
	TokenAuditUsed:    "api_token.used",
	TokenAuditRevoked: "api_token.revoked",
}

// TokenAuditAction is the TokenAudit* an activity action stands for, "" when
// it stands for none.
func TokenAuditAction(action string) string {
	for audit, a := range tokenActions {
		if a == action {
			return audit
		}
	}
	return ""
}

// recordToken records a token event in the token's tenant ("" for a global
// application, recorded as the platform's); the client's address and agent
// go in Changes.
func recordToken(ctx context.Context, tx activity.DBTX, tenantID, audit string, tokenID, appID uuid.UUID, ip, userAgent string) error {
	changes := map[string][2]any{}
	if ip != "" {
		changes["ip"] = [2]any{nil, ip}
	}
	if userAgent != "" {
		changes["userAgent"] = [2]any{nil, userAgent}
	}
	return activity.Record(ctx, tx, activity.Event{
		TenantID: tenantID, Platform: tenantID == "", Action: tokenActions[audit],
		Target:  activity.Ref{Kind: KindAPIToken, ID: tokenID.String()},
		Scope:   &activity.Ref{Kind: "client_application", ID: appID.String()},
		Changes: changes,
	})
}

func (s *ClientApplicationService) inTx(ctx context.Context, fn func(q *repository.Queries, tx pgx.Tx) error) error {
	tx, err := s.store.ConnPool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("client application: begin: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := fn(repository.New(tx), tx); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// APITokenKind lets a token's activity be read by the administrators of its
// tenant (super administrators for a global application's).
type APITokenKind struct{}

func (APITokenKind) Kind() string { return KindAPIToken }

func (APITokenKind) CanRead(_ context.Context, tenantID string, p access.Principal, _ string) (bool, error) {
	if tenantID == "" {
		return p.HasRole(access.RoleSuperAdmin), nil
	}
	return p.TenantID == tenantID && p.IsTenantAdmin(), nil
}
