package handlers

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"iag-warehouse/backend/internal/middleware"
	"iag-warehouse/backend/internal/store"
)

// Handling units (licence plates). Addressed by LPN rather than by id
// throughout, because the LPN is what is printed on the plate and what a
// terminal has after a scan.

func (a *API) ListHandlingUnits(c *gin.Context) {
	items, err := a.Store.ListHandlingUnits(c.Request.Context(), c.Query("status"), c.Query("bin_code"), 100)
	if err != nil {
		storeErr(c, err)
		return
	}
	ok(c, gin.H{"items": items})
}

func (a *API) GetHandlingUnit(c *gin.Context) {
	hu, err := a.Store.GetHandlingUnitByLPN(c.Request.Context(), c.Param("lpn"))
	if err != nil {
		storeErr(c, err)
		return
	}
	ok(c, hu)
}

func (a *API) CreateHandlingUnit(c *gin.Context) {
	var body struct {
		LPN     string         `json:"lpn"`
		HUType  string         `json:"hu_type"`
		BinCode string         `json:"bin_code"`
		Attrs   map[string]any `json:"attrs"`
	}
	if err := bindJSONCoerced(c, &body); err != nil {
		badRequest(c, "invalid JSON")
		return
	}
	in := store.CreateHUInput{LPN: body.LPN, HUType: body.HUType, BinCode: body.BinCode, Attrs: body.Attrs}
	if uid, okUID := middleware.UserID(c); okUID {
		in.CreatedBy = &uid
	}
	a.withIdempotency(c, func() (int, any) {
		hu, err := a.Store.CreateHandlingUnit(c.Request.Context(), in)
		if err != nil {
			return statusForStoreErr(err), gin.H{"error": messageForStoreErr(err)}
		}
		return http.StatusCreated, hu
	})
}

// huContentBody is the shared shape of load and unload.
type huContentBody struct {
	ItemID    string  `json:"item_id"`
	ItemSKU   string  `json:"item_sku"`
	LotKey    string  `json:"lot_key"`
	SerialKey string  `json:"serial_key"`
	Qty       float64 `json:"qty"`
	UOM       string  `json:"uom"`
}

func (a *API) LoadHandlingUnit(c *gin.Context) {
	a.huContentHandler(c, true)
}

func (a *API) UnloadHandlingUnit(c *gin.Context) {
	a.huContentHandler(c, false)
}

func (a *API) huContentHandler(c *gin.Context, load bool) {
	hu, err := a.Store.GetHandlingUnitByLPN(c.Request.Context(), c.Param("lpn"))
	if err != nil {
		storeErr(c, err)
		return
	}
	var body huContentBody
	if err := bindJSONCoerced(c, &body); err != nil {
		badRequest(c, "invalid JSON")
		return
	}
	itemID, err := a.resolveItemID(c.Request.Context(), body.ItemID, body.ItemSKU)
	if err != nil {
		badRequest(c, "item_id or item_sku is required")
		return
	}
	conv, err := a.Store.ConvertToBase(c.Request.Context(), itemID, body.Qty, body.UOM)
	if err != nil {
		storeErr(c, err)
		return
	}

	var out any
	if load {
		out, err = a.Store.AddToHandlingUnit(c.Request.Context(), hu.ID, itemID, body.LotKey, body.SerialKey, conv.BaseQty)
	} else {
		out, err = a.Store.RemoveFromHandlingUnit(c.Request.Context(), hu.ID, itemID, body.LotKey, body.SerialKey, conv.BaseQty)
	}
	if err != nil {
		storeErr(c, err)
		return
	}
	ok(c, out)
}

func (a *API) MoveHandlingUnit(c *gin.Context) {
	hu, err := a.Store.GetHandlingUnitByLPN(c.Request.Context(), c.Param("lpn"))
	if err != nil {
		storeErr(c, err)
		return
	}
	var body struct {
		ToBinCode string `json:"to_bin_code"`
	}
	if err := bindJSONCoerced(c, &body); err != nil || body.ToBinCode == "" {
		badRequest(c, "to_bin_code is required")
		return
	}
	var actorID *uuid.UUID
	if uid, okUID := middleware.UserID(c); okUID {
		actorID = &uid
	}
	moved, err := a.Store.MoveHandlingUnit(c.Request.Context(), hu.ID, body.ToBinCode, actorID)
	if err != nil {
		storeErr(c, err)
		return
	}
	ok(c, moved)
}

// NestHandlingUnit sets or clears a plate's parent. An empty parent_lpn
// un-nests it, which is how a carton comes off a pallet.
func (a *API) NestHandlingUnit(c *gin.Context) {
	hu, err := a.Store.GetHandlingUnitByLPN(c.Request.Context(), c.Param("lpn"))
	if err != nil {
		storeErr(c, err)
		return
	}
	var body struct {
		ParentLPN string `json:"parent_lpn"`
	}
	if err := bindJSONCoerced(c, &body); err != nil {
		badRequest(c, "invalid JSON")
		return
	}
	nested, err := a.Store.NestHandlingUnit(c.Request.Context(), hu.ID, body.ParentLPN)
	if err != nil {
		storeErr(c, err)
		return
	}
	ok(c, nested)
}

func (a *API) SetHandlingUnitStatus(c *gin.Context) {
	hu, err := a.Store.GetHandlingUnitByLPN(c.Request.Context(), c.Param("lpn"))
	if err != nil {
		storeErr(c, err)
		return
	}
	var body struct {
		Status string `json:"status"`
	}
	if err := bindJSONCoerced(c, &body); err != nil {
		badRequest(c, "invalid JSON")
		return
	}
	updated, err := a.Store.SetHandlingUnitStatus(c.Request.Context(), hu.ID, body.Status)
	if err != nil {
		storeErr(c, err)
		return
	}
	ok(c, updated)
}
