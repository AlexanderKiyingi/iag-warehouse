package handlers

import (
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"iag-warehouse/backend/internal/middleware"
	"iag-warehouse/backend/internal/store"
)

func (a *API) CreateTransfer(c *gin.Context) {
	var body struct {
		FromFacilityCode string `json:"from_facility_code"`
		ToFacilityCode   string `json:"to_facility_code"`
		Notes            string `json:"notes"`
		Lines            []struct {
			ItemID      string  `json:"item_id"`
			ItemSKU     string  `json:"item_sku"`
			Qty         float64 `json:"qty"`
			FromBinCode string  `json:"from_bin_code"`
			ToBinCode   string  `json:"to_bin_code"`
			LotKey      string  `json:"lot_key"`
			SerialKey   string  `json:"serial_key"`
		} `json:"lines"`
	}
	if err := bindJSONCoerced(c, &body); err != nil || len(body.Lines) == 0 {
		badRequest(c, "lines are required")
		return
	}
	var lines []store.TransferLineInput
	for _, l := range body.Lines {
		itemID, err := a.resolveItemID(c.Request.Context(), l.ItemID, l.ItemSKU)
		if err != nil {
			badRequest(c, err.Error())
			return
		}
		// A line that names no bin takes the facility default at each end, so a
		// caller that thinks in sites rather than bins can still move stock.
		fromBin, err := a.resolveBinCode(c.Request.Context(), l.FromBinCode, body.FromFacilityCode)
		if err != nil {
			badRequest(c, "from: "+err.Error())
			return
		}
		toBin, err := a.resolveBinCode(c.Request.Context(), l.ToBinCode, body.ToFacilityCode)
		if err != nil {
			badRequest(c, "to: "+err.Error())
			return
		}
		lines = append(lines, store.TransferLineInput{
			ItemID: itemID, Qty: l.Qty, FromBinCode: fromBin, ToBinCode: toBin,
			LotKey: l.LotKey, SerialKey: l.SerialKey,
		})
	}
	var createdBy *uuid.UUID
	if uid, ok := middleware.UserID(c); ok {
		createdBy = &uid
	}
	a.withIdempotency(c, func() (int, any) {
		tr, err := a.Store.CreateTransfer(c.Request.Context(), store.CreateTransferInput{
			FromFacilityCode: strPtr(body.FromFacilityCode),
			ToFacilityCode:   strPtr(body.ToFacilityCode),
			Notes:            strPtr(body.Notes),
			Lines:            lines,
			CreatedBy:        createdBy,
		})
		if err != nil {
			if err == store.ErrInsufficientStock {
				return http.StatusUnprocessableEntity, gin.H{"error": "insufficient stock"}
			}
			return http.StatusInternalServerError, gin.H{"error": err.Error()}
		}
		return http.StatusCreated, tr
	})
}

func (a *API) CreateAdjustment(c *gin.Context) {
	a.adjustmentHandler(c, false)
}

func (a *API) CreateCycleCount(c *gin.Context) {
	a.adjustmentHandler(c, true)
}

func (a *API) adjustmentHandler(c *gin.Context, cycle bool) {
	var body struct {
		ItemID   string `json:"item_id"`
		ItemSKU  string `json:"item_sku"`
		BinCode  string `json:"bin_code"`
		// FacilityCode stands in for BinCode when the caller knows the site but
		// not the bin — see resolveBinCode.
		FacilityCode string   `json:"facility_code"`
		LotKey       string   `json:"lot_key"`
		SerialKey    string   `json:"serial_key"`
		QtyAfter     *float64 `json:"qty_after"`
		QtyDelta     *float64 `json:"qty_delta"`
		Reason       string   `json:"reason"`
	}
	if err := bindJSONCoerced(c, &body); err != nil {
		badRequest(c, "invalid JSON")
		return
	}
	// Exactly one quantity. Accepting both would leave the caller's intent
	// ambiguous on the one field that decides what the stock becomes.
	if (body.QtyAfter == nil) == (body.QtyDelta == nil) {
		badRequest(c, "send exactly one of qty_after (the counted quantity) or qty_delta (the movement)")
		return
	}
	// A manual adjustment must carry a reason (audit trail for shrinkage/damage/
	// recount). A cycle count is self-justifying — the physical count is the reason.
	if !cycle && strings.TrimSpace(body.Reason) == "" {
		badRequest(c, "reason is required for a stock adjustment")
		return
	}
	itemID, err := a.resolveItemID(c.Request.Context(), body.ItemID, body.ItemSKU)
	if err != nil {
		badRequest(c, err.Error())
		return
	}
	binCode, err := a.resolveBinCode(c.Request.Context(), body.BinCode, body.FacilityCode)
	if err != nil {
		badRequest(c, err.Error())
		return
	}
	var actorID *uuid.UUID
	if uid, ok := middleware.UserID(c); ok {
		actorID = &uid
	}
	in := store.AdjustmentInput{
		ItemID: itemID, BinCode: binCode, LotKey: body.LotKey, SerialKey: body.SerialKey,
		QtyDelta: body.QtyDelta, Reason: strPtr(body.Reason), ActorID: actorID,
	}
	if body.QtyAfter != nil {
		in.QtyAfter = *body.QtyAfter
	}
	a.withIdempotency(c, func() (int, any) {
		var adj any
		var err error
		if cycle {
			adj, err = a.Store.CreateCycleCount(c.Request.Context(), in)
		} else {
			adj, err = a.Store.CreateAdjustment(c.Request.Context(), in)
		}
		if err != nil {
			return http.StatusInternalServerError, gin.H{"error": err.Error()}
		}
		return http.StatusCreated, adj
	})
}
