package service

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/cto-up/core-lib/api/openapi/core"
	"github.com/cto-up/core-lib/pkg/core/db"
	"github.com/cto-up/core-lib/pkg/core/db/repository"
	sharedservice "github.com/cto-up/core-lib/pkg/shared/service"
)

// ActivityContributor puts the activity log under a member's data-protection
// rights (office#199): their export lists what they did; deleting their
// account takes their name off every row and keeps the row, which is the
// organisation's record of what happened.
type ActivityContributor struct {
	store *db.Store
}

func NewActivityContributor(store *db.Store) sharedservice.UserDataContributor {
	return &ActivityContributor{store: store}
}

func (c *ActivityContributor) Name() string { return "activity" }

// LeaveImpacts reports nothing: the log is the organisation's, not a member's
// to hand over.
func (c *ActivityContributor) LeaveImpacts(context.Context, string, string) ([]core.LeaveImpact, error) {
	return []core.LeaveImpact{}, nil
}

func (c *ActivityContributor) ApplyLeaveDecisions(context.Context, string, string, []core.LeaveDecision) error {
	return nil
}

// ActivityExport is one row as the member's export shows it.
type ActivityExport struct {
	TenantID   string    `json:"tenantId"`
	OccurredAt time.Time `json:"occurredAt"`
	Via        string    `json:"via"`
	Action     string    `json:"action"`
	Target     string    `json:"target"`
}

func (c *ActivityContributor) Export(ctx context.Context, userID string) (any, error) {
	rows, err := c.store.ListActivityByActor(ctx, pgtype.Text{String: userID, Valid: true})
	if err != nil {
		return nil, fmt.Errorf("activity.Export: %w", err)
	}
	out := make([]ActivityExport, len(rows))
	for i, r := range rows {
		out[i] = ActivityExport{TenantID: r.TenantID, OccurredAt: r.OccurredAt, Via: r.Via, Action: r.Action, Target: r.TargetKind + ":" + r.TargetID}
	}
	return out, nil
}

// Erase forgets who acted when the account is deleted. Leaving a tenant
// (dormancy) keeps the organisation's record as it is.
func (c *ActivityContributor) Erase(ctx context.Context, userID, _ string, scope sharedservice.EraseScope) error {
	if scope != sharedservice.EraseAccountDeletion {
		return nil
	}
	tx, err := c.store.ConnPool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("activity.Erase: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	q := repository.New(tx)
	if err := q.AllowActivityMaintenance(ctx); err != nil {
		return fmt.Errorf("activity.Erase: %w", err)
	}
	if _, err := q.ForgetActivityActor(ctx, pgtype.Text{String: userID, Valid: true}); err != nil {
		return fmt.Errorf("activity.Erase: %w", err)
	}
	return tx.Commit(ctx)
}
