//go:build testutils

package core

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"

	"github.com/cto-up/core-lib/api/openapi/core"
	"github.com/cto-up/core-lib/pkg/core/db/testutils"
	"github.com/cto-up/core-lib/pkg/shared/access"
	"github.com/cto-up/core-lib/pkg/shared/activity"
	"github.com/cto-up/core-lib/pkg/shared/auth"
)

type docKind struct{}

func (docKind) Kind() string { return "doc" }
func (docKind) CanRead(_ context.Context, _ string, p access.Principal, id string) (bool, error) {
	return p.UserID == "ada" && id == "d1", nil
}

func TestActivityAnswersWithTheStatedCodes_Integration(t *testing.T) {
	store := testutils.NewTestStore(t)
	activity.RegisterKind(docKind{})
	ctx := access.WithPrincipal(context.Background(), access.Principal{TenantID: "t1", UserID: "ada", Door: access.DoorWeb})
	require.NoError(t, activity.Record(ctx, store.ConnPool, activity.Event{TenantID: "t1", Action: "doc.created", Target: activity.Ref{Kind: "doc", ID: "d1"},
		Changes: map[string][2]any{"title": {nil, "Plan"}}}))
	h := NewActivityHandler(store)
	call := func(user string, do func(*gin.Context)) *httptest.ResponseRecorder {
		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		c.Request = httptest.NewRequest(http.MethodGet, "/", nil).WithContext(
			access.WithPrincipal(context.Background(), access.Principal{TenantID: "t1", UserID: user}))
		c.Set(auth.AUTH_TENANT_ID_KEY, "t1")
		do(c)
		return w
	}
	s := func(v string) *string { return &v }

	w := call("ada", func(c *gin.Context) {
		h.ListActivity(c, core.ListActivityParams{TargetKind: s("doc"), TargetId: s("d1")})
	})
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	var got []core.ActivityEntry
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &got))
	require.Len(t, got, 1)
	require.Equal(t, "doc.created", got[0].Action)
	require.Equal(t, []interface{}{nil, "Plan"}, (*got[0].Changes)["title"])

	require.Equal(t, http.StatusNotFound, call("bob", func(c *gin.Context) {
		h.ListActivity(c, core.ListActivityParams{TargetKind: s("doc"), TargetId: s("d1")})
	}).Code, "bob cannot read d1")
	require.Equal(t, http.StatusNotFound, call("ada", func(c *gin.Context) {
		h.ListActivity(c, core.ListActivityParams{TargetKind: s("nope"), TargetId: s("x")})
	}).Code, "an unknown kind")
	require.Equal(t, http.StatusBadRequest, call("ada", func(c *gin.Context) { h.ListActivity(c, core.ListActivityParams{}) }).Code)
	require.Equal(t, http.StatusBadRequest, call("ada", func(c *gin.Context) {
		h.ListActivity(c, core.ListActivityParams{TargetKind: s("doc"), TargetId: s("d1"), ScopeKind: s("doc"), ScopeId: s("d1")})
	}).Code, "both a target and a scope")

	w = call("bob", func(c *gin.Context) { h.ListTenantActivity(c, core.ListTenantActivityParams{Kind: s("doc")}) })
	require.Equal(t, http.StatusOK, w.Code)
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &got))
	require.Len(t, got, 1, "the admin log reads every kind (the /admin-api door checks the role)")
}
