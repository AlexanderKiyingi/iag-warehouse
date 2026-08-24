package handlers

import (
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"iag-warehouse/backend/internal/events"
	"iag-warehouse/backend/internal/middleware"
	"iag-warehouse/backend/internal/models"
	"iag-warehouse/backend/internal/store"
)

func (a *API) ListAssets(c *gin.Context) {
	items, err := a.Store.ListAssets(c.Request.Context())
	if err != nil {
		storeErr(c, err)
		return
	}
	ok(c, gin.H{"items": items})
}

func (a *API) CreateAsset(c *gin.Context) {
	var body struct {
		AssetTag  string         `json:"asset_tag"`
		SerialNo  string         `json:"serial_no"`
		ItemID    string         `json:"item_id"`
		BinCode   string         `json:"bin_code"`
		Condition string         `json:"condition"`
		Attrs     map[string]any `json:"attrs"`
	}
	if err := c.ShouldBindJSON(&body); err != nil || body.AssetTag == "" || body.ItemID == "" {
		badRequest(c, "asset_tag and item_id are required")
		return
	}
	itemID, err := uuid.Parse(body.ItemID)
	if err != nil {
		badRequest(c, "invalid item_id")
		return
	}
	if body.Condition == "" {
		body.Condition = "good"
	}
	a.withIdempotency(c, func() (int, any) {
		asset, err := a.Store.CreateAsset(c.Request.Context(), body.AssetTag, strPtr(body.SerialNo), itemID, strPtr(body.BinCode), body.Condition, body.Attrs)
		if err != nil {
			return http.StatusInternalServerError, gin.H{"error": err.Error()}
		}
		return http.StatusCreated, asset
	})
}

func (a *API) CheckInAsset(c *gin.Context) {
	var body struct {
		BinCode string `json:"bin_code"`
	}
	if err := c.ShouldBindJSON(&body); err != nil || body.BinCode == "" {
		badRequest(c, "bin_code is required")
		return
	}
	var actorID *uuid.UUID
	if uid, ok := middleware.UserID(c); ok {
		actorID = &uid
	}
	a.withIdempotency(c, func() (int, any) {
		asset, err := a.Store.CheckInAsset(c.Request.Context(), c.Param("tag"), body.BinCode, actorID)
		if err != nil {
			return http.StatusInternalServerError, gin.H{"error": err.Error()}
		}
		return http.StatusOK, asset
	})
}

func (a *API) CheckOutAsset(c *gin.Context) {
	var body struct {
		ToDepartment string `json:"to_department"`
		Custodian    string `json:"custodian"`
		Notes        string `json:"notes"`
	}
	if err := c.ShouldBindJSON(&body); err != nil || body.ToDepartment == "" {
		badRequest(c, "to_department is required")
		return
	}
	var actorID *uuid.UUID
	if uid, ok := middleware.UserID(c); ok {
		actorID = &uid
	}
	a.withIdempotency(c, func() (int, any) {
		asset, err := a.Store.CheckOutAsset(c.Request.Context(), store.CheckOutAssetInput{
			AssetTag: c.Param("tag"), ToDepartment: body.ToDepartment,
			Custodian: body.Custodian, Notes: body.Notes, ActorID: actorID,
		})
		if err != nil {
			return http.StatusInternalServerError, gin.H{"error": err.Error()}
		}
		return http.StatusOK, asset
	})
}

var validDisposalMethods = map[string]bool{
	"sale": true, "scrap": true, "donation": true, "trade_in": true, "write_off": true, "lost": true,
}

func (a *API) DisposeAsset(c *gin.Context) {
	var body struct {
		Method       string   `json:"method"`
		Reason       string   `json:"reason"`
		Proceeds     float64  `json:"proceeds"`
		Currency     string   `json:"currency"`
		BookValue    *float64 `json:"book_value"`
		GatePassNo   string   `json:"gate_pass_no"`
		AuthorizedBy string   `json:"authorized_by"`
	}
	if err := bindJSONCoerced(c, &body); err != nil || !validDisposalMethods[body.Method] {
		badRequest(c, "method is required (sale, scrap, donation, trade_in, write_off, lost)")
		return
	}
	if strings.TrimSpace(body.Reason) == "" {
		badRequest(c, "reason is required for an asset disposal")
		return
	}
	var actorID *uuid.UUID
	if uid, ok := middleware.UserID(c); ok {
		actorID = &uid
	}
	a.withIdempotency(c, func() (int, any) {
		d, err := a.Store.DisposeAsset(c.Request.Context(), store.DisposeAssetInput{
			AssetTag: c.Param("tag"), Method: body.Method, Reason: body.Reason,
			Proceeds: body.Proceeds, Currency: body.Currency, BookValue: body.BookValue,
			GatePassNo: body.GatePassNo, AuthorizedBy: body.AuthorizedBy,
			RequestedBy: middleware.ActorEmail(c), ActorID: actorID,
		}, a.Cfg.RequireDisposalApproval)
		if err != nil {
			switch err {
			case store.ErrNotFound:
				return http.StatusNotFound, gin.H{"error": "asset not found"}
			case store.ErrConflict:
				return http.StatusConflict, gin.H{"error": "asset is already disposed or has an open disposal"}
			default:
				return http.StatusInternalServerError, gin.H{"error": err.Error()}
			}
		}
		return http.StatusCreated, d
	})
}

func (a *API) ListDisposals(c *gin.Context) {
	items, err := a.Store.ListDisposals(c.Request.Context(), c.Query("status"), 100)
	if err != nil {
		storeErr(c, err)
		return
	}
	ok(c, gin.H{"items": items})
}

func (a *API) ListDisposalApprovalTiers(c *gin.Context) {
	tiers, err := a.Store.ListDisposalApprovalTiers(c.Request.Context())
	if err != nil {
		storeErr(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"tiers": tiers})
}

func (a *API) ApproveDisposal(c *gin.Context) {
	a.decideDisposal(c, true)
}

func (a *API) RejectDisposal(c *gin.Context) {
	a.decideDisposal(c, false)
}

func (a *API) decideDisposal(c *gin.Context, approve bool) {
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		badRequest(c, "invalid disposal id")
		return
	}
	var body struct {
		Note string `json:"note"`
	}
	_ = c.ShouldBindJSON(&body)
	hasPerm := func(code string) bool { return middleware.HasPerm(c, code) }
	actor := middleware.ActorEmail(c)
	a.withIdempotency(c, func() (int, any) {
		var (
			d        any
			progress any
			err      error
		)
		if approve {
			row, p, e := a.Store.ApproveDisposal(c.Request.Context(), id, actor, hasPerm, strings.TrimSpace(body.Note))
			d, progress, err = row, p, e
			if e == nil {
				a.notifyDisposalDecision(c, row, p, true)
			}
		} else {
			row, p, e := a.Store.RejectDisposal(c.Request.Context(), id, actor, hasPerm, strings.TrimSpace(body.Note))
			d, progress, err = row, p, e
			if e == nil {
				a.notifyDisposalDecision(c, row, p, false)
			}
		}
		if err != nil {
			switch {
			case errors.Is(err, store.ErrNotFound):
				return http.StatusNotFound, gin.H{"error": "disposal not found"}
			case errors.Is(err, store.ErrForbidden):
				return http.StatusForbidden, gin.H{"error": err.Error()}
			case errors.Is(err, store.ErrInvalidArgument):
				return http.StatusConflict, gin.H{"error": err.Error()}
			default:
				return http.StatusInternalServerError, gin.H{"error": err.Error()}
			}
		}
		return http.StatusOK, gin.H{"disposal": d, "approval": progress}
	})
}

// notifyDisposalDecision emails the outcome of an asset-disposal approval.
//
// The requester is addressable here — DisposeAsset records RequestedBy from
// the caller's token email — so a decision goes straight to them. A partial
// approval still needing a further tier goes to the ops desk instead: the next
// approver is a permission code, not a person, so there is no one to write to.
//
// Best effort: the decision is committed, and a notification problem must not
// turn it into an error.
func (a *API) notifyDisposalDecision(c *gin.Context, d models.AssetDisposal, prog *store.DisposalApprovalProgress, approved bool) {
	if a.Bus == nil || !a.Bus.Enabled() {
		return
	}
	ctx := c.Request.Context()
	what := "Disposal of asset " + d.AssetTag + " (" + d.Method + ")"

	if approved && prog != nil && !prog.Complete {
		desk := events.DefaultNotifyRecipient()
		tier := ""
		if prog.NextTier != nil {
			tier = " tier " + strconv.Itoa(*prog.NextTier)
		}
		a.Bus.PublishAlertTo(ctx, "", "approvals.warehouse", desk, "approval.pending", map[string]string{
			"Title": "Disposal awaiting" + tier + ": " + d.AssetTag,
			"Body": what + " requested by " + d.RequestedBy + " has cleared " +
				strconv.Itoa(len(prog.ApprovedTiers)) + " of " + strconv.Itoa(len(prog.RequiredTiers)) +
				" tiers and needs " + prog.NextPerm + ".",
		}, d.ID.String())
		return
	}

	// The requester is a specific person, so this decision carries NO audience:
	// an audience resolves to whoever an admin has put on the warehouse desk,
	// which would redirect a personal notification away from the person it is
	// about. Only the desk fallback is routable.
	recipient := strings.TrimSpace(d.RequestedBy)
	audience := ""
	if recipient == "" {
		recipient = events.DefaultNotifyRecipient()
		audience = "approvals.warehouse"
	}
	outcome := "rejected"
	if approved {
		outcome = "approved"
	}
	a.Bus.PublishAlertTo(ctx, "", audience, recipient, "approval.decision", map[string]string{
		"Title": "Disposal " + outcome + ": " + d.AssetTag,
		"Body":  what + " was " + outcome + ".",
	}, d.ID.String())
}

func (a *API) SparePartsByAsset(c *gin.Context) {
	items, err := a.Store.ListSparePartsByAsset(c.Request.Context(), c.Param("asset_tag"))
	if err != nil {
		storeErr(c, err)
		return
	}
	ok(c, gin.H{"items": items})
}
