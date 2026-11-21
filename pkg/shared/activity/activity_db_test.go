//go:build testutils

package activity_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/cto-up/core-lib/pkg/core/db"
	"github.com/cto-up/core-lib/pkg/core/db/testutils"
	coreservice "github.com/cto-up/core-lib/pkg/core/service"
	"github.com/cto-up/core-lib/pkg/shared/access"
	"github.com/cto-up/core-lib/pkg/shared/activity"
	sharedservice "github.com/cto-up/core-lib/pkg/shared/service"
	"github.com/cto-up/core-lib/pkg/shared/util"
)

// pageKind lets the reader "ada" read every page but "secret".
type pageKind struct{}

func (pageKind) Kind() string { return "page" }
func (pageKind) CanRead(_ context.Context, tenantID string, p access.Principal, id string) (bool, error) {
	return tenantID == "t1" && p.UserID == "ada" && id != "secret", nil
}

type notebookKind struct{}

func (notebookKind) Kind() string { return "notebook" }
func (notebookKind) CanRead(_ context.Context, _ string, p access.Principal, _ string) (bool, error) {
	return p.UserID == "ada", nil
}

func web(user string) context.Context {
	ctx := context.WithValue(context.Background(), util.RequestIDKey, "req-1")
	return access.WithPrincipal(ctx, access.Principal{TenantID: "t1", UserID: user, Door: access.DoorWeb})
}

func record(t *testing.T, store *db.Store, ctx context.Context, e activity.Event) {
	t.Helper()
	require.NoError(t, activity.Record(ctx, store.ConnPool, e))
}

func newStore(t *testing.T) *db.Store {
	store := testutils.NewTestStore(t)
	_, err := store.ConnPool.Exec(context.Background(),
		`INSERT INTO core_users (id, email, profile) VALUES ('ada', 'ada@acme.com', '{"name": "Ada Lovelace"}'), ('bob', 'bob@acme.com', '{}')`)
	require.NoError(t, err)
	activity.RegisterKind(pageKind{})
	activity.RegisterKind(notebookKind{})
	return store
}

func TestAnEventIsRecordedWithWhoAndHowFromTheRequestAndRollsBackWithTheChange_Integration(t *testing.T) {
	store := newStore(t)
	ctx := web("ada")
	nb := activity.Ref{Kind: "notebook", ID: "billing"}

	record(t, store, ctx, activity.Event{TenantID: "t1", Action: "page.renamed", Target: activity.Ref{Kind: "page", ID: "p1"},
		Scope: &nb, Changes: map[string][2]any{"title": {"Refunds", "Refund policy"}}})
	got, err := activity.List(ctx, store.ConnPool, "t1", access.Principal{UserID: "ada"}, activity.Query{Target: &activity.Ref{Kind: "page", ID: "p1"}})
	require.NoError(t, err)
	require.Len(t, got, 1)
	e := got[0]
	require.Equal(t, "ada", e.ActorID)
	require.Equal(t, "Ada Lovelace", e.ActorName, "the name as it was when they acted")
	require.Equal(t, access.DoorWeb, e.Via)
	require.Equal(t, "page.renamed", e.Action)
	require.Equal(t, &nb, e.Scope)
	require.Equal(t, map[string][2]any{"title": {"Refunds", "Refund policy"}}, e.Changes)
	var reqID string
	require.NoError(t, store.ConnPool.QueryRow(context.Background(), `SELECT request_id FROM core_activity WHERE id = $1`, e.ID).Scan(&reqID))
	require.Equal(t, "req-1", reqID)

	tx, err := store.ConnPool.Begin(ctx)
	require.NoError(t, err)
	require.NoError(t, activity.Record(ctx, tx, activity.Event{TenantID: "t1", Action: "page.moved", Target: activity.Ref{Kind: "page", ID: "p1"}}))
	require.NoError(t, tx.Rollback(ctx))
	got, err = activity.List(ctx, store.ConnPool, "t1", access.Principal{UserID: "ada"}, activity.Query{Target: &activity.Ref{Kind: "page", ID: "p1"}})
	require.NoError(t, err)
	require.Len(t, got, 1, "a change that rolls back leaves no row")

	key := access.WithPrincipal(context.Background(), access.Principal{TenantID: "t1", UserID: "bob", Door: access.DoorAPIKey, KeyID: "tok-1"})
	record(t, store, key, activity.Event{TenantID: "t1", Action: "page.written", Target: activity.Ref{Kind: "page", ID: "p2"}})
	record(t, store, context.Background(), activity.Event{TenantID: "t1", Action: "page.archived", Target: activity.Ref{Kind: "page", ID: "p2"}})
	system := access.WithPrincipal(context.Background(), access.Principal{TenantID: "t1", UserID: "bob", System: true})
	record(t, store, system, activity.Event{TenantID: "t1", Action: "page.restored", Target: activity.Ref{Kind: "page", ID: "p2"}})
	undeclared := access.WithPrincipal(context.Background(), access.Principal{TenantID: "t1", UserID: "bob"})
	record(t, store, undeclared, activity.Event{TenantID: "t1", Action: "page.deleted", Target: activity.Ref{Kind: "page", ID: "p2"}})
	got, err = activity.List(ctx, store.ConnPool, "t1", access.Principal{UserID: "ada"}, activity.Query{Target: &activity.Ref{Kind: "page", ID: "p2"}})
	require.NoError(t, err)
	require.Len(t, got, 4)
	require.Equal(t, [4]string{activity.ViaUnknown, access.DoorJob, access.DoorJob, access.DoorAPIKey},
		[4]string{got[0].Via, got[1].Via, got[2].Via, got[3].Via}, "newest first: a door that does not say, a system job, no principal, an API key")
	require.Equal(t, "tok-1", got[3].KeyID)
	require.Equal(t, "bob@acme.com", got[3].ActorName, "a person with no name shows by email")
	require.Empty(t, got[1].ActorID, "a job has no actor")

	require.Error(t, activity.Record(ctx, store.ConnPool, activity.Event{Action: "page.moved", Target: activity.Ref{Kind: "page", ID: "p1"}}), "no tenant")
	require.Error(t, activity.Record(ctx, store.ConnPool, activity.Event{TenantID: "t1", Target: activity.Ref{Kind: "page", ID: "p1"}}), "no action")
}

func TestOnlyWhatTheKindLetsTheCallerReadIsListed_Integration(t *testing.T) {
	store := newStore(t)
	ctx := web("ada")
	nb := activity.Ref{Kind: "notebook", ID: "billing"}
	for _, id := range []string{"p1", "p2", "p3"} {
		record(t, store, ctx, activity.Event{TenantID: "t1", Action: "page.created", Target: activity.Ref{Kind: "page", ID: id}, Scope: &nb})
	}
	record(t, store, ctx, activity.Event{TenantID: "t1", Action: "page.created", Target: activity.Ref{Kind: "page", ID: "secret"}})
	record(t, store, ctx, activity.Event{TenantID: "t2", Action: "page.created", Target: activity.Ref{Kind: "page", ID: "p1"}})
	record(t, store, ctx, activity.Event{TenantID: "t1", Action: "card.created", Target: activity.Ref{Kind: "card", ID: "c1"}})
	ada, bob := access.Principal{UserID: "ada"}, access.Principal{UserID: "bob"}

	_, err := activity.List(ctx, store.ConnPool, "t1", ada, activity.Query{Target: &activity.Ref{Kind: "page", ID: "secret"}})
	require.ErrorIs(t, err, activity.ErrNotFound)
	_, err = activity.List(ctx, store.ConnPool, "t1", bob, activity.Query{Target: &activity.Ref{Kind: "page", ID: "p1"}})
	require.ErrorIs(t, err, activity.ErrNotFound)
	_, err = activity.List(ctx, store.ConnPool, "t1", ada, activity.Query{Target: &activity.Ref{Kind: "card", ID: "c1"}})
	require.ErrorIs(t, err, activity.ErrNotFound, "a kind nobody registered")
	_, err = activity.List(ctx, store.ConnPool, "t1", ada, activity.Query{})
	require.Error(t, err, "neither target nor scope")
	require.False(t, errors.Is(err, activity.ErrNotFound))

	page1, err := activity.List(ctx, store.ConnPool, "t1", ada, activity.Query{Scope: &nb, Limit: 2})
	require.NoError(t, err)
	require.Len(t, page1, 2)
	require.Equal(t, "p3", page1[0].Target.ID)
	page2, err := activity.List(ctx, store.ConnPool, "t1", ada, activity.Query{Scope: &nb, Limit: 2, Before: &page1[1].ID})
	require.NoError(t, err)
	require.Len(t, page2, 1)
	require.Equal(t, "p1", page2[0].Target.ID)

	all, err := activity.ListTenant(ctx, store.ConnPool, "t1", activity.Query{})
	require.NoError(t, err)
	require.Len(t, all, 5, "the tenant's rows of every kind, never another tenant's")
	cards, err := activity.ListTenant(ctx, store.ConnPool, "t1", activity.Query{Kind: "card"})
	require.NoError(t, err)
	require.Len(t, cards, 1)
	future := time.Now().Add(time.Hour)
	none, err := activity.ListTenant(ctx, store.ConnPool, "t1", activity.Query{From: &future})
	require.NoError(t, err)
	require.Empty(t, none)
	byBob, err := activity.ListTenant(ctx, store.ConnPool, "t1", activity.Query{ActorID: "bob", Action: "page.created"})
	require.NoError(t, err)
	require.Empty(t, byBob)
}

func TestRowsCannotBeChangedAndErasureTakesOnlyTheName_Integration(t *testing.T) {
	store := newStore(t)
	ctx := web("ada")
	record(t, store, ctx, activity.Event{TenantID: "t1", Action: "page.created", Target: activity.Ref{Kind: "page", ID: "p1"}})
	bg := context.Background()

	_, err := store.ConnPool.Exec(bg, `UPDATE core_activity SET action = 'page.deleted'`)
	require.ErrorContains(t, err, "append-only")
	_, err = store.ConnPool.Exec(bg, `DELETE FROM core_activity`)
	require.ErrorContains(t, err, "append-only")

	c := coreservice.NewActivityContributor(store)
	exported, err := c.Export(bg, "ada")
	require.NoError(t, err)
	require.Len(t, exported, 1)

	require.NoError(t, c.Erase(bg, "ada", "t1", sharedservice.EraseTenantDormancy))
	got, err := activity.ListTenant(bg, store.ConnPool, "t1", activity.Query{})
	require.NoError(t, err)
	require.Equal(t, "ada", got[0].ActorID, "leaving a tenant keeps the organisation's record")

	require.NoError(t, c.Erase(bg, "ada", "", sharedservice.EraseAccountDeletion))
	got, err = activity.ListTenant(bg, store.ConnPool, "t1", activity.Query{})
	require.NoError(t, err)
	require.Len(t, got, 1, "the row stays")
	require.Empty(t, got[0].ActorID)
	require.Empty(t, got[0].ActorName)
	require.Equal(t, "page.created", got[0].Action)
	_, err = store.ConnPool.Exec(bg, `UPDATE core_activity SET actor_id = 'x'`)
	require.ErrorContains(t, err, "append-only", "erasure's permission ends with its transaction")
}

func TestDiffListsTheFieldsThatChanged(t *testing.T) {
	type page struct {
		Title string
		Type  string
		Owner *string
		ID    uuid.UUID
	}
	ada := "ada"
	require.Equal(t,
		map[string][2]any{"title": {"Refunds", "Refund policy"}, "owner": {(*string)(nil), &ada}},
		activity.Diff(page{Title: "Refunds", Type: "note"}, &page{Title: "Refund policy", Type: "note", Owner: &ada}, "Title", "Type", "Owner", "Missing"))
}
