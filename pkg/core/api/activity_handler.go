package core

import (
	"errors"
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/cto-up/core-lib/api/helpers"
	"github.com/cto-up/core-lib/api/openapi/core"
	"github.com/cto-up/core-lib/pkg/core/db"
	"github.com/cto-up/core-lib/pkg/shared/access"
	"github.com/cto-up/core-lib/pkg/shared/activity"
	"github.com/cto-up/core-lib/pkg/shared/auth"
	"github.com/cto-up/core-lib/pkg/shared/util"
)

// ActivityHandler serves the activity log (office#199).
type ActivityHandler struct {
	store *db.Store
}

func NewActivityHandler(store *db.Store) *ActivityHandler {
	return &ActivityHandler{store: store}
}

// ListActivity implements core.ServerInterface: one target's or one scope's
// activity, if the kind that owns it lets the caller read it.
func (h *ActivityHandler) ListActivity(c *gin.Context, params core.ListActivityParams) {
	tenantID := c.GetString(auth.AUTH_TENANT_ID_KEY)
	if tenantID == "" {
		c.JSON(http.StatusForbidden, helpers.ErrorStringResponse("a tenant is required"))
		return
	}
	target, scope := activityRef(params.TargetKind, params.TargetId), activityRef(params.ScopeKind, params.ScopeId)
	if (target == nil) == (scope == nil) {
		c.JSON(http.StatusBadRequest, helpers.ErrorStringResponse("give one target (targetKind, targetId) or one scope (scopeKind, scopeId)"))
		return
	}
	p, _ := access.PrincipalFrom(c.Request.Context())
	rows, err := activity.List(c.Request.Context(), h.store.ConnPool, tenantID, p, activity.Query{
		Target: target, Scope: scope, Before: params.Before, Limit: intOf(params.Limit),
	})
	if errors.Is(err, activity.ErrNotFound) {
		c.JSON(http.StatusNotFound, helpers.ErrorStringResponse("not found"))
		return
	}
	if err != nil {
		logger := util.GetLoggerFromCtx(c.Request.Context())
		logger.Err(err).Msg("Failed to list activity")
		c.JSON(http.StatusInternalServerError, helpers.ErrorResponse(err))
		return
	}
	c.JSON(http.StatusOK, activityToAPI(rows))
}

// ListTenantActivity implements core.ServerInterface: the host tenant's log,
// for its administrators (the /admin-api door checks the role).
func (h *ActivityHandler) ListTenantActivity(c *gin.Context, params core.ListTenantActivityParams) {
	tenantID := c.GetString(auth.AUTH_TENANT_ID_KEY)
	if tenantID == "" {
		c.JSON(http.StatusForbidden, helpers.ErrorStringResponse("a tenant is required"))
		return
	}
	rows, err := activity.ListTenant(c.Request.Context(), h.store.ConnPool, tenantID, activity.Query{
		Kind: strOf(params.Kind), Action: strOf(params.Action), ActorID: strOf(params.ActorId),
		From: params.From, To: params.To, Before: params.Before, Limit: intOf(params.Limit),
	})
	if err != nil {
		logger := util.GetLoggerFromCtx(c.Request.Context())
		logger.Err(err).Msg("Failed to list the tenant's activity")
		c.JSON(http.StatusInternalServerError, helpers.ErrorResponse(err))
		return
	}
	c.JSON(http.StatusOK, activityToAPI(rows))
}

func activityRef(kind, id *string) *activity.Ref {
	if kind == nil || id == nil || *kind == "" || *id == "" {
		return nil
	}
	return &activity.Ref{Kind: *kind, ID: *id}
}

func intOf(n *int32) int {
	if n == nil {
		return 0
	}
	return int(*n)
}

func strOf(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

func optStr(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

func activityToAPI(rows []activity.Entry) []core.ActivityEntry {
	out := make([]core.ActivityEntry, len(rows))
	for i, r := range rows {
		e := core.ActivityEntry{
			Id: r.ID, OccurredAt: r.OccurredAt, ActorId: optStr(r.ActorID), ActorName: optStr(r.ActorName),
			Via: r.Via, KeyId: optStr(r.KeyID), Action: r.Action,
			Target: core.ActivityRef{Kind: r.Target.Kind, Id: r.Target.ID},
		}
		if r.Scope != nil {
			e.Scope = &core.ActivityRef{Kind: r.Scope.Kind, Id: r.Scope.ID}
		}
		if len(r.Changes) > 0 {
			ch := make(map[string][]interface{}, len(r.Changes))
			for k, v := range r.Changes {
				ch[k] = []interface{}{v[0], v[1]}
			}
			e.Changes = &ch
		}
		out[i] = e
	}
	return out
}
