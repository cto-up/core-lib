package service

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/cto-up/core-lib/api/openapi/core"
	"github.com/cto-up/core-lib/pkg/core/db"
	"github.com/cto-up/core-lib/pkg/core/db/repository"
	"github.com/cto-up/core-lib/pkg/shared/mailer"
	sharedservice "github.com/cto-up/core-lib/pkg/shared/service"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

// EmailDeliveriesContributor puts the email delivery log under a member's
// data-protection rights (ADR 090 §4). A row is found by the HMAC of the
// member's address, so the address must still be readable when Erase runs —
// which the lifecycle guarantees: contributors erase before core_users goes.
type EmailDeliveriesContributor struct {
	store *db.Store
}

func NewEmailDeliveriesContributor(store *db.Store) sharedservice.UserDataContributor {
	return &EmailDeliveriesContributor{store: store}
}

func (c *EmailDeliveriesContributor) Name() string { return "email_deliveries" }

// LeaveImpacts reports nothing: a log of mail sent to the member is nobody's to
// inherit.
func (c *EmailDeliveriesContributor) LeaveImpacts(context.Context, string, string) ([]core.LeaveImpact, error) {
	return []core.LeaveImpact{}, nil
}

func (c *EmailDeliveriesContributor) ApplyLeaveDecisions(context.Context, string, string, []core.LeaveDecision) error {
	return nil
}

// EmailDeliveryExport is one row as the member's export shows it.
type EmailDeliveryExport struct {
	TenantID  string    `json:"tenantId,omitempty"`
	SentAt    time.Time `json:"sentAt"`
	Kind      string    `json:"kind"`
	Recipient string    `json:"recipient"`
	Role      string    `json:"role"`
	Status    string    `json:"status"`
}

// Export returns the member's rows in each of their tenants and in platform
// mail.
func (c *EmailDeliveriesContributor) Export(ctx context.Context, userID string) (any, error) {
	email, tenants, err := c.subject(ctx, userID)
	if err != nil || email == "" {
		return []EmailDeliveryExport{}, err
	}
	out := []EmailDeliveryExport{}
	for _, tenantID := range append(tenants, "") {
		mac, err := mailer.RecipientHMAC(tenantID, email)
		if err != nil {
			return nil, fmt.Errorf("email_deliveries.Export: %w", err)
		}
		rows, err := c.store.ListEmailDeliveriesForRecipient(ctx, repository.ListEmailDeliveriesForRecipientParams{
			TenantID: pgtype.Text{String: tenantID, Valid: tenantID != ""}, RecipientHmac: mac,
		})
		if err != nil {
			return nil, fmt.Errorf("email_deliveries.Export: %w", err)
		}
		for _, r := range rows {
			out = append(out, EmailDeliveryExport{
				TenantID: tenantID, SentAt: r.CreatedAt, Kind: r.Kind,
				Recipient: r.RecipientMasked, Role: r.RecipientRole, Status: r.Status,
			})
		}
	}
	return out, nil
}

// Erase deletes the member's rows in the tenant left (dormancy), or in every
// tenant and in platform mail (account deletion).
func (c *EmailDeliveriesContributor) Erase(ctx context.Context, userID, tenantID string, scope sharedservice.EraseScope) error {
	email, tenants, err := c.subject(ctx, userID)
	if err != nil {
		return fmt.Errorf("email_deliveries.Erase: %w", err)
	}
	if email == "" {
		return nil
	}
	var targets []string
	switch scope {
	case sharedservice.EraseTenantDormancy:
		if tenantID == "" {
			return errors.New("email_deliveries.Erase: dormancy erase needs a tenant")
		}
		targets = []string{tenantID}
	case sharedservice.EraseAccountDeletion:
		targets = append(tenants, "")
	default:
		return fmt.Errorf("email_deliveries.Erase: unknown erase scope %q", scope)
	}
	for _, t := range targets {
		if _, err := EraseEmailDeliveries(ctx, c.store, t, email); err != nil {
			return fmt.Errorf("email_deliveries.Erase: %w", err)
		}
	}
	return nil
}

// EraseEmailDeliveries deletes one address's rows in one tenant ("" is
// platform mail) and returns how many went.
func EraseEmailDeliveries(ctx context.Context, store *db.Store, tenantID, address string) (int64, error) {
	mac, err := mailer.RecipientHMAC(tenantID, address)
	if err != nil {
		return 0, err
	}
	return store.DeleteEmailDeliveriesForRecipient(ctx, repository.DeleteEmailDeliveriesForRecipientParams{
		TenantID: pgtype.Text{String: tenantID, Valid: tenantID != ""}, RecipientHmac: mac,
	})
}

func (c *EmailDeliveriesContributor) subject(ctx context.Context, userID string) (string, []string, error) {
	user, err := c.store.GetSharedUserByID(ctx, userID)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", nil, nil
	}
	if err != nil {
		return "", nil, err
	}
	memberships, err := c.store.ListAllUserTenantMemberships(ctx, userID)
	if err != nil {
		return "", nil, err
	}
	tenants := make([]string, 0, len(memberships))
	for _, m := range memberships {
		tenants = append(tenants, m.TenantID)
	}
	return user.Email.String, tenants, nil
}
