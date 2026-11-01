package service

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/cto-up/core-lib/api/openapi/core"
	"github.com/cto-up/core-lib/pkg/core/db/repository"
	"github.com/cto-up/core-lib/pkg/shared/mailer"
	"github.com/cto-up/core-lib/pkg/shared/util"
	"github.com/jackc/pgx/v5"
	openapi_types "github.com/oapi-codegen/runtime/types"
)

const (
	// InactiveAccountMonthsConfig is the core_global_configs key holding how many
	// months without a request close an account. 0 disables the rule.
	InactiveAccountMonthsConfig = "core.retention.inactive_account_months"

	// DefaultInactiveAccountMonths applies while the key is absent: three years,
	// the period the CNIL commonly accepts for an account nobody uses.
	DefaultInactiveAccountMonths = 36

	// InactivityDeletionReason is the deletion_reason of an account closed by
	// this rule rather than by its owner.
	InactivityDeletionReason = "inactivity"

	// InactivityWarningMonths is how long before closing an account the
	// warning is sent; a still-quiet account is closed that long after it.
	InactivityWarningMonths = 1

	inactivityWarningTemplate = "account-inactive-warning.html"

	adminClosureTemplate = "account-closure-scheduled-by-admin.html"
)

// lastSeenTrackedSince is when core_users.last_seen_at started being stamped
// (migration 20260822174405). An account never stamped has been quiet at most
// since then, whatever its created_at says.
var lastSeenTrackedSince = time.Date(2026, 8, 22, 0, 0, 0, 0, time.UTC)

// ProcessInactiveAccounts warns accounts a month before they reach the
// configured inactivity period, and closes those warned a month ago that are
// still quiet. Closing is ScheduleDeletion: the grace period, the cancel path
// and every contributor's erasure apply exactly as when the owner closes it.
func (s *MembershipLifecycleService) ProcessInactiveAccounts(ctx context.Context, now time.Time) error {
	logger := util.GetLoggerFromCtx(ctx)

	months, err := s.inactiveAccountMonths(ctx)
	if err != nil {
		return err
	}
	if months == 0 {
		return nil
	}

	due, err := s.store.ListUsersDueForInactivityDeletion(ctx, repository.ListUsersDueForInactivityDeletionParams{
		WarnedBefore: now.AddDate(0, -InactivityWarningMonths, 0),
		TrackedSince: lastSeenTrackedSince,
		Cutoff:       now.AddDate(0, -months, 0),
		RowLimit:     sweepPage,
	})
	if err != nil {
		return err
	}
	reason := InactivityDeletionReason
	scheduled := 0
	for _, u := range due {
		if _, err := s.ScheduleDeletion(ctx, u.ID, core.AccountDeletionRequest{
			ConfirmEmail: openapi_types.Email(u.Email.String),
			Reason:       &reason,
		}); err != nil {
			logger.Err(err).Str("user_id", u.ID).Msg("inactivity: scheduling the deletion failed, will retry on the next run")
			continue
		}
		scheduled++
	}

	toWarn, err := s.store.ListUsersToWarnOfInactivity(ctx, repository.ListUsersToWarnOfInactivityParams{
		TrackedSince: lastSeenTrackedSince,
		Cutoff:       now.AddDate(0, -(months - InactivityWarningMonths), 0),
		RowLimit:     sweepPage,
	})
	if err != nil {
		return err
	}
	warned := 0
	for _, u := range toWarn {
		claimed, err := s.store.MarkInactivityWarned(ctx, u.ID)
		if err != nil {
			logger.Err(err).Str("user_id", u.ID).Msg("inactivity: could not stamp the warning")
			continue
		}
		if claimed == 0 {
			continue // another replica took it
		}
		closesOn := lastActivity(u).AddDate(0, months, 0)
		if earliest := now.AddDate(0, InactivityWarningMonths, 0); closesOn.Before(earliest) {
			closesOn = earliest
		}
		if err := s.warnInactive(ctx, u, months-InactivityWarningMonths, closesOn); err != nil {
			logger.Err(err).Str("user_id", u.ID).Msg("inactivity: warning not sent, will retry on the next run")
			if err := s.store.ClearInactivityWarning(ctx, u.ID); err != nil {
				logger.Err(err).Str("user_id", u.ID).Msg("inactivity: could not withdraw the unsent warning")
			}
			continue
		}
		warned++
	}

	if scheduled > 0 || warned > 0 {
		logger.Info().Int("warned", warned).Int("scheduled", scheduled).Msg("lifecycle sweep: inactive accounts processed")
	}
	return nil
}

func (s *MembershipLifecycleService) inactiveAccountMonths(ctx context.Context) (int, error) {
	cfg, err := s.store.GetGlobalConfigByName(ctx, InactiveAccountMonthsConfig)
	if errors.Is(err, pgx.ErrNoRows) {
		return DefaultInactiveAccountMonths, nil
	}
	if err != nil {
		return 0, err
	}
	months, err := strconv.Atoi(strings.TrimSpace(cfg.Value.String))
	// A period no longer than the warning would warn and close in the same breath.
	if err != nil || months < 0 || (months > 0 && months <= InactivityWarningMonths) {
		return 0, fmt.Errorf("%s = %q: want 0 or a number of months above %d", InactiveAccountMonthsConfig, cfg.Value.String, InactivityWarningMonths)
	}
	return months, nil
}

func lastActivity(u repository.CoreUser) time.Time {
	if u.LastSeenAt.Valid {
		return u.LastSeenAt.Time
	}
	if u.CreatedAt.After(lastSeenTrackedSince) {
		return u.CreatedAt
	}
	return lastSeenTrackedSince
}

func sendInactivityWarning(ctx context.Context, u repository.CoreUser, monthsInactive int, closesOn time.Time) error {
	data := struct {
		Name           string
		MonthsInactive int
		ClosesOn       string
	}{u.Profile.Name, monthsInactive, closesOn.Format("2 January 2006")}
	body, err := mailer.RenderTemplate(filepath.Join("templates", inactivityWarningTemplate), data)
	if err != nil {
		return err
	}
	return mailer.Send(ctx, mailer.SystemAccount(), mailer.Message{Kind: "core.inactive_warning", To: []string{u.Email.String},
		Subject: "Your account will be closed for inactivity", Body: body, HTML: true})
}

type adminClosureData struct {
	Name         string
	Organization string
	DeletesOn    string
	KeepURL      string
}

func sendAdminClosureNotice(ctx context.Context, u repository.CoreUser, closure AdminClosure, deletesOn time.Time) error {
	data := adminClosureData{u.Profile.Name, closure.TenantName, deletesOn.Format("2 January 2006"), closure.KeepURL}
	body, err := mailer.RenderTemplate(filepath.Join("templates", adminClosureTemplate), data)
	if err != nil {
		return err
	}
	return mailer.Send(ctx, mailer.SystemAccount(), mailer.Message{Kind: "core.admin_closure", To: []string{u.Email.String},
		Subject: "An administrator closed your account", Body: body, HTML: true})
}
