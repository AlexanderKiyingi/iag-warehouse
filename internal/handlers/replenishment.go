package handlers

import (
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"iag-warehouse/backend/internal/middleware"
	"iag-warehouse/backend/internal/store"
)

// Replenishment levels and tasks.

func (a *API) ListReplenLevels(c *gin.Context) {
	items, err := a.Store.ListReplenLevels(c.Request.Context())
	if err != nil {
		storeErr(c, err)
		return
	}
	ok(c, gin.H{"items": items})
}

func (a *API) UpsertReplenLevel(c *gin.Context) {
	var body struct {
		ItemID         string  `json:"item_id"`
		ItemSKU        string  `json:"item_sku"`
		BinCode        string  `json:"bin_code"`
		MinQty         float64 `json:"min_qty"`
		MaxQty         float64 `json:"max_qty"`
		SourceZoneCode string  `json:"source_zone_code"`
		Active         *bool   `json:"active"`
	}
	if err := bindJSONCoerced(c, &body); err != nil {
		badRequest(c, "invalid JSON")
		return
	}
	ctx := c.Request.Context()

	itemID, err := a.resolveItemID(ctx, body.ItemID, body.ItemSKU)
	if err != nil {
		badRequest(c, "item_id or item_sku is required")
		return
	}
	bin, _, err := a.Store.GetBinByCode(ctx, strings.TrimSpace(body.BinCode))
	if err != nil {
		badRequest(c, "unknown bin_code")
		return
	}
	in := store.ReplenLevelInput{
		ItemID: itemID,
		BinID:  bin.ID,
		MinQty: body.MinQty,
		MaxQty: body.MaxQty,
		Active: body.Active == nil || *body.Active,
	}
	if s := strings.TrimSpace(body.SourceZoneCode); s != "" {
		z, zerr := a.Store.GetZoneByCode(ctx, s)
		if zerr != nil {
			badRequest(c, "unknown source_zone_code")
			return
		}
		in.SourceZoneID = &z.ID
	}
	if uid, okUID := middleware.UserID(c); okUID {
		in.CreatedBy = &uid
	}

	level, err := a.Store.UpsertReplenLevel(ctx, in)
	if err != nil {
		storeErr(c, err)
		return
	}
	created(c, level)
}

func (a *API) DeleteReplenLevel(c *gin.Context) {
	id, okID := parsePathID(c)
	if !okID {
		return
	}
	if err := a.Store.DeleteReplenLevel(c.Request.Context(), id); err != nil {
		storeErr(c, err)
		return
	}
	c.Status(http.StatusNoContent)
}

func (a *API) ListReplenTasks(c *gin.Context) {
	items, err := a.Store.ListReplenTasks(c.Request.Context(), c.Query("status"), 100)
	if err != nil {
		storeErr(c, err)
		return
	}
	ok(c, gin.H{"items": items})
}

func (a *API) CreateReplenTask(c *gin.Context) {
	var body struct {
		ItemID      string  `json:"item_id"`
		ItemSKU     string  `json:"item_sku"`
		FromBinCode string  `json:"from_bin_code"`
		ToBinCode   string  `json:"to_bin_code"`
		LotKey      string  `json:"lot_key"`
		Qty         float64 `json:"qty"`
		UOM         string  `json:"uom"`
		Notes       string  `json:"notes"`
	}
	if err := bindJSONCoerced(c, &body); err != nil {
		badRequest(c, "invalid JSON")
		return
	}
	ctx := c.Request.Context()

	itemID, err := a.resolveItemID(ctx, body.ItemID, body.ItemSKU)
	if err != nil {
		badRequest(c, "item_id or item_sku is required")
		return
	}
	fromBin, _, err := a.Store.GetBinByCode(ctx, strings.TrimSpace(body.FromBinCode))
	if err != nil {
		badRequest(c, "unknown from_bin_code")
		return
	}
	toBin, _, err := a.Store.GetBinByCode(ctx, strings.TrimSpace(body.ToBinCode))
	if err != nil {
		badRequest(c, "unknown to_bin_code")
		return
	}
	conv, err := a.Store.ConvertToBase(ctx, itemID, body.Qty, body.UOM)
	if err != nil {
		storeErr(c, err)
		return
	}
	in := store.ReplenTaskInput{
		ItemID:    itemID,
		FromBinID: fromBin.ID,
		ToBinID:   toBin.ID,
		LotKey:    body.LotKey,
		Qty:       conv.BaseQty,
		Trigger:   "manual",
		Notes:     strPtr(body.Notes),
	}
	if uid, okUID := middleware.UserID(c); okUID {
		in.CreatedBy = &uid
	}

	a.withIdempotency(c, func() (int, any) {
		task, terr := a.Store.CreateReplenTask(ctx, in)
		if terr != nil {
			return statusForStoreErr(terr), gin.H{"error": terr.Error()}
		}
		return http.StatusCreated, task
	})
}

// GenerateReplenTasks is the sweep a scheduler calls: every pick face below its
// minimum gets an instruction raised against it.
func (a *API) GenerateReplenTasks(c *gin.Context) {
	var actorID *uuid.UUID
	if uid, okUID := middleware.UserID(c); okUID {
		actorID = &uid
	}
	tasks, err := a.Store.GenerateReplenTasks(c.Request.Context(), actorID)
	if err != nil {
		storeErr(c, err)
		return
	}
	ok(c, gin.H{"items": tasks, "generated": len(tasks)})
}

func (a *API) CompleteReplenTask(c *gin.Context) {
	id, okID := parsePathID(c)
	if !okID {
		return
	}
	var body struct {
		MovedQty *float64 `json:"moved_qty"`
	}
	// A body is optional: completing with nothing said means the whole task moved.
	_ = bindJSONCoerced(c, &body)

	var actorID *uuid.UUID
	if uid, okUID := middleware.UserID(c); okUID {
		actorID = &uid
	}
	task, err := a.Store.CompleteReplenTask(c.Request.Context(), id, body.MovedQty, actorID)
	if err != nil {
		storeErr(c, err)
		return
	}
	ok(c, task)
}

func (a *API) CancelReplenTask(c *gin.Context) {
	id, okID := parsePathID(c)
	if !okID {
		return
	}
	var actorID *uuid.UUID
	if uid, okUID := middleware.UserID(c); okUID {
		actorID = &uid
	}
	task, err := a.Store.CancelReplenTask(c.Request.Context(), id, actorID)
	if err != nil {
		storeErr(c, err)
		return
	}
	ok(c, task)
}
