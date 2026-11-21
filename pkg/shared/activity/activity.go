// Package activity is one log, for every app, of who did what through which
// door (office#199). An app records an Event in the same transaction as the
// change it describes, so a change that rolls back leaves no row; who acted
// and how come from the request's access.Principal, never from the caller.
//
// The package knows no domain: an app registers each kind it records (a
// page, a card) with the question only it can answer, "may this caller read
// it?", and every read goes through that question.
package activity

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/cto-up/core-lib/pkg/core/db/repository"
	"github.com/cto-up/core-lib/pkg/shared/access"
	"github.com/cto-up/core-lib/pkg/shared/util"
)

// Ref names a thing of a registered kind.
type Ref struct {
	Kind string
	ID   string
}

// Event is one change. Action is "<kind>.<past tense>" ("page.moved"); Scope,
// optional, is what a feed lists it under and is checked for scope reads;
// Changes holds small fields only, never a body.
type Event struct {
	TenantID string
	Action   string
	Target   Ref
	Scope    *Ref
	Changes  map[string][2]any
}

// DBTX is what Record writes through: the caller's transaction (a pgx.Tx
// satisfies it), so the row commits or rolls back with the change.
type DBTX = repository.DBTX

var (
	errNoTenant = errors.New("activity: an event needs a tenant")
	errNoAction = errors.New("activity: an event needs an action and a target")
)

// ViaUnknown is how a call came when its door does not say.
const ViaUnknown = "unknown"

// Record writes e through tx with who and how from ctx: the Principal's user
// and door (DoorJob when there is none or it is a system one, ViaUnknown when
// the door does not say), its key, and the request id.
func Record(ctx context.Context, tx DBTX, e Event) error {
	if e.TenantID == "" {
		return errNoTenant
	}
	if e.Action == "" || e.Target.Kind == "" || e.Target.ID == "" {
		return errNoAction
	}
	var changes []byte
	if len(e.Changes) > 0 {
		raw, err := json.Marshal(e.Changes)
		if err != nil {
			return fmt.Errorf("activity.Record: %w", err)
		}
		changes = raw
	}
	actor, via, key := who(ctx)
	params := repository.InsertActivityParams{
		TenantID: e.TenantID, ActorID: text(actor), Via: via, KeyID: text(key),
		Action: e.Action, TargetKind: e.Target.Kind, TargetID: e.Target.ID,
		Changes: changes, RequestID: text(requestID(ctx)),
	}
	if e.Scope != nil {
		params.ScopeKind, params.ScopeID = text(e.Scope.Kind), text(e.Scope.ID)
	}
	if err := repository.New(tx).InsertActivity(ctx, params); err != nil {
		return fmt.Errorf("activity.Record: %w", err)
	}
	return nil
}

func who(ctx context.Context) (actor, via, key string) {
	p, ok := access.PrincipalFrom(ctx)
	if !ok || p.System {
		return "", access.DoorJob, ""
	}
	via = p.Door
	if via == "" {
		// A door that does not say yet: never guess one.
		via = ViaUnknown
	}
	return p.UserID, via, p.KeyID
}

func requestID(ctx context.Context) string {
	id, _ := ctx.Value(util.RequestIDKey).(string)
	return id
}

func text(s string) pgtype.Text { return pgtype.Text{String: s, Valid: s != ""} }

// Kind is what an app registers for each kind it records: whether the caller
// may read a thing of that kind. Only what CanRead allows is ever listed,
// outside the tenant administrators' log.
type Kind interface {
	Kind() string
	CanRead(ctx context.Context, tenantID string, p access.Principal, id string) (bool, error)
}

var (
	kindsMu sync.RWMutex
	kinds   = map[string]Kind{}
)

// RegisterKind installs k at boot; registering a kind again replaces it.
func RegisterKind(k Kind) {
	kindsMu.Lock()
	defer kindsMu.Unlock()
	kinds[k.Kind()] = k
}

func kindOf(name string) (Kind, bool) {
	kindsMu.RLock()
	defer kindsMu.RUnlock()
	k, ok := kinds[name]
	return k, ok
}

// ErrNotFound answers a read of a thing the caller cannot read, or of a kind
// nobody registered: the same answer as a thing that does not exist.
var ErrNotFound = errors.New("activity: not found")

// Entry is one row as a reader sees it.
type Entry struct {
	ID         uuid.UUID
	OccurredAt time.Time
	ActorID    string
	ActorName  string
	Via        string
	KeyID      string
	Action     string
	Target     Ref
	Scope      *Ref
	Changes    map[string][2]any
}

// Query is what to list: one target, or one scope, or (Admin) the tenant's,
// narrowed by kind, action, actor and time; Before pages.
type Query struct {
	Target  *Ref
	Scope   *Ref
	Kind    string
	Action  string
	ActorID string
	From    *time.Time
	To      *time.Time
	Before  *uuid.UUID
	Limit   int
}

// MaxLimit is the most rows a page holds.
const MaxLimit = 100

// List reads a target's or a scope's activity for p, after asking the kind
// whether p may read it.
func List(ctx context.Context, db DBTX, tenantID string, p access.Principal, q Query) ([]Entry, error) {
	ref := q.Target
	if ref == nil {
		ref = q.Scope
	}
	if ref == nil || (q.Target != nil && q.Scope != nil) {
		return nil, errors.New("activity: list one target or one scope")
	}
	k, ok := kindOf(ref.Kind)
	if !ok {
		return nil, ErrNotFound
	}
	can, err := k.CanRead(ctx, tenantID, p, ref.ID)
	if err != nil {
		return nil, fmt.Errorf("activity.List: %w", err)
	}
	if !can {
		return nil, ErrNotFound
	}
	return read(ctx, db, tenantID, Query{Target: q.Target, Scope: q.Scope, Before: q.Before, Limit: q.Limit})
}

// ListTenant reads the tenant's whole log: for tenant administrators, whom
// the caller has checked.
func ListTenant(ctx context.Context, db DBTX, tenantID string, q Query) ([]Entry, error) {
	q.Target, q.Scope = nil, nil
	return read(ctx, db, tenantID, q)
}

func read(ctx context.Context, db DBTX, tenantID string, q Query) ([]Entry, error) {
	limit := q.Limit
	if limit <= 0 || limit > MaxLimit {
		limit = MaxLimit
	}
	params := repository.ListActivityParams{
		TenantID: tenantID, Kind: text(q.Kind), Action: text(q.Action), ActorID: text(q.ActorID),
		RowLimit: int32(limit),
	}
	if q.Target != nil {
		params.TargetKind, params.TargetID = text(q.Target.Kind), text(q.Target.ID)
	}
	if q.Scope != nil {
		params.ScopeKind, params.ScopeID = text(q.Scope.Kind), text(q.Scope.ID)
	}
	if q.From != nil {
		params.FromAt = pgtype.Timestamptz{Time: *q.From, Valid: true}
	}
	if q.To != nil {
		params.ToAt = pgtype.Timestamptz{Time: *q.To, Valid: true}
	}
	if q.Before != nil {
		params.Before = pgtype.UUID{Bytes: *q.Before, Valid: true}
	}
	rows, err := repository.New(db).ListActivity(ctx, params)
	if err != nil {
		return nil, fmt.Errorf("activity.read: %w", err)
	}
	out := make([]Entry, len(rows))
	for i, r := range rows {
		e := Entry{
			ID: r.ID, OccurredAt: r.OccurredAt, ActorID: r.ActorID.String, ActorName: r.ActorName.String,
			Via: r.Via, KeyID: r.KeyID.String, Action: r.Action, Target: Ref{Kind: r.TargetKind, ID: r.TargetID},
		}
		if r.ScopeKind.Valid {
			e.Scope = &Ref{Kind: r.ScopeKind.String, ID: r.ScopeID.String}
		}
		if len(r.Changes) > 0 {
			if err := json.Unmarshal(r.Changes, &e.Changes); err != nil {
				return nil, fmt.Errorf("activity.read: %w", err)
			}
		}
		out[i] = e
	}
	return out, nil
}

// Diff lists the named fields that differ between before and after, two
// values of the same struct type, by field name: what Event.Changes holds.
// A field is named by its Go name; the key is its name with a lower first
// letter.
func Diff(before, after any, fields ...string) map[string][2]any {
	b, a := reflect.Indirect(reflect.ValueOf(before)), reflect.Indirect(reflect.ValueOf(after))
	out := map[string][2]any{}
	for _, f := range fields {
		bv, av := b.FieldByName(f), a.FieldByName(f)
		if !bv.IsValid() || !av.IsValid() {
			continue
		}
		x, y := bv.Interface(), av.Interface()
		if !reflect.DeepEqual(x, y) {
			out[strings.ToLower(f[:1])+f[1:]] = [2]any{x, y}
		}
	}
	return out
}
