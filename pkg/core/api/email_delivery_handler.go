package core

import (
	"net/http"
	"strings"
	"time"

	"github.com/cto-up/core-lib/api/helpers"
	"github.com/cto-up/core-lib/api/openapi/core"
	"github.com/cto-up/core-lib/pkg/core/db"
	"github.com/cto-up/core-lib/pkg/core/db/repository"
	coreservice "github.com/cto-up/core-lib/pkg/core/service"
	"github.com/cto-up/core-lib/pkg/shared/auth"
	"github.com/cto-up/core-lib/pkg/shared/mailer"
	sharedservice "github.com/cto-up/core-lib/pkg/shared/service"
	"github.com/cto-up/core-lib/pkg/shared/util"
	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5/pgtype"
)

const defaultSummaryWindow = 7 * 24 * time.Hour

// EmailDeliveryHandler serves the email delivery log (ADR 090 §5).
type EmailDeliveryHandler struct {
	store *db.Store
}

func NewEmailDeliveryHandler(store *db.Store) *EmailDeliveryHandler {
	return &EmailDeliveryHandler{store: store}
}

// ListEmailDeliveries implements core.ServerInterface. Rows are the host
// tenant's only; there is no cross-tenant listing, the super admin gets counts.
func (h *EmailDeliveryHandler) ListEmailDeliveries(c *gin.Context, params core.ListEmailDeliveriesParams) {
	logger := util.GetLoggerFromCtx(c.Request.Context())

	tenantID := c.GetString(auth.AUTH_TENANT_ID_KEY)
	if tenantID == "" {
		c.JSON(http.StatusForbidden, helpers.ErrorStringResponse("a tenant is required"))
		return
	}

	var recipient []byte
	if params.Address != nil && strings.TrimSpace(*params.Address) != "" {
		mac, err := mailer.RecipientHMAC(tenantID, *params.Address)
		if err != nil {
			logger.Err(err).Msg("Cannot search the email delivery log by address")
			c.JSON(http.StatusServiceUnavailable, helpers.ErrorStringResponse("the email delivery log is not configured"))
			return
		}
		recipient = mac
		h.recordMemberSearch(c, tenantID, *params.Address)
	}

	var status pgtype.Text
	if params.Status != nil {
		status = pgtype.Text{String: string(*params.Status), Valid: true}
	}
	limit, offset := adminPage(params.Page, params.PageSize)
	rows, err := h.store.ListEmailDeliveries(c, repository.ListEmailDeliveriesParams{
		TenantID:      pgtype.Text{String: tenantID, Valid: true},
		Status:        status,
		Kind:          util.ToNullableText(params.Kind),
		SentFrom:      util.ToNullableTimestamptz(params.From),
		SentTo:        util.ToNullableTimestamptz(params.To),
		RecipientHmac: recipient,
		RowLimit:      limit,
		RowOffset:     offset,
	})
	if err != nil {
		logger.Err(err).Msg("Failed to list email deliveries")
		c.JSON(http.StatusInternalServerError, helpers.ErrorResponse(err))
		return
	}

	out := make([]core.EmailDelivery, len(rows))
	for i, r := range rows {
		out[i] = core.EmailDelivery{
			Id:              r.ID,
			CreatedAt:       r.CreatedAt,
			Kind:            r.Kind,
			Account:         r.Account,
			RecipientMasked: r.RecipientMasked,
			RecipientRole:   core.EmailDeliveryRecipientRole(r.RecipientRole),
			Status:          core.EmailDeliveryStatus(r.Status),
			OriginType:      textPtr(r.OriginType),
			OriginId:        textPtr(r.OriginID),
			ErrorClass:      textPtr(r.ErrorClass),
			MessageId:       textPtr(r.MessageID),
		}
		if r.SmtpCode.Valid {
			code := r.SmtpCode.Int32
			out[i].SmtpCode = &code
		}
	}
	c.JSON(http.StatusOK, out)
}

// EraseEmailDeliveries implements core.ServerInterface: an administrator
// answering an erasure request from someone who is not a member.
func (h *EmailDeliveryHandler) EraseEmailDeliveries(c *gin.Context, params core.EraseEmailDeliveriesParams) {
	logger := util.GetLoggerFromCtx(c.Request.Context())
	tenantID := c.GetString(auth.AUTH_TENANT_ID_KEY)
	if tenantID == "" {
		c.JSON(http.StatusForbidden, helpers.ErrorStringResponse("a tenant is required"))
		return
	}
	if strings.TrimSpace(params.Address) == "" {
		c.JSON(http.StatusBadRequest, helpers.ErrorStringResponse("address is required"))
		return
	}
	n, err := coreservice.EraseEmailDeliveries(c, h.store, tenantID, params.Address)
	if err != nil {
		logger.Err(err).Msg("Failed to erase email deliveries")
		c.JSON(http.StatusInternalServerError, helpers.ErrorResponse(err))
		return
	}
	logger.Info().Str("tenant_id", tenantID).Int64("rows", n).Msg("email deliveries erased for one address")
	c.Status(http.StatusNoContent)
}

// recordMemberSearch logs a search for a member's address as a read of their
// personal data (core ADR 002). An address that is no member's names nobody
// the log could record.
func (h *EmailDeliveryHandler) recordMemberSearch(c *gin.Context, tenantID, address string) {
	user, err := h.store.GetUserByEmailGlobal(c, mailer.NormalizeAddress(address))
	if err != nil {
		return
	}
	if _, err := h.store.GetSharedUserTenantMembership(c, repository.GetSharedUserTenantMembershipParams{
		UserID: user.ID, TenantID: tenantID,
	}); err != nil {
		return
	}
	sharedservice.RecordPersonalDataAccess(c.Request.Context(), sharedservice.PersonalDataAccess{
		TenantID:      tenantID,
		ActorUserID:   c.GetString(auth.AUTH_USER_ID),
		SubjectUserID: user.ID,
		Resource:      "email_deliveries.search",
	})
}

// SummarizeEmailDeliveries implements core.ServerInterface: counts across every
// tenant, never a row or an address.
func (h *EmailDeliveryHandler) SummarizeEmailDeliveries(c *gin.Context, params core.SummarizeEmailDeliveriesParams) {
	logger := util.GetLoggerFromCtx(c.Request.Context())
	if !auth.IsSuperAdmin(c) {
		c.JSON(http.StatusForbidden, helpers.ErrorStringResponse("super admin required"))
		return
	}

	to := time.Now()
	if params.To != nil {
		to = *params.To
	}
	from := to.Add(-defaultSummaryWindow)
	if params.From != nil {
		from = *params.From
	}
	rows, err := h.store.SummarizeEmailDeliveries(c, repository.SummarizeEmailDeliveriesParams{SentFrom: from, SentTo: to})
	if err != nil {
		logger.Err(err).Msg("Failed to summarize email deliveries")
		c.JSON(http.StatusInternalServerError, helpers.ErrorResponse(err))
		return
	}
	out := make([]core.EmailDeliveryCount, len(rows))
	for i, r := range rows {
		out[i] = core.EmailDeliveryCount{Kind: r.Kind, Status: core.EmailDeliveryStatus(r.Status), Count: r.Count}
	}
	c.JSON(http.StatusOK, out)
}

func textPtr(t pgtype.Text) *string {
	if !t.Valid {
		return nil
	}
	s := t.String
	return &s
}
