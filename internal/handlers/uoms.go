package handlers

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"iag-warehouse/backend/internal/store"
)

// Item unit-of-measure conversions.

func (a *API) ListItemUOMs(c *gin.Context) {
	itemID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		badRequest(c, "invalid item id")
		return
	}
	items, err := a.Store.ListItemUOMs(c.Request.Context(), itemID)
	if err != nil {
		storeErr(c, err)
		return
	}
	ok(c, gin.H{"items": items})
}

func (a *API) UpsertItemUOM(c *gin.Context) {
	itemID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		badRequest(c, "invalid item id")
		return
	}
	var body struct {
		UOM               string  `json:"uom"`
		Factor            float64 `json:"factor"`
		IsPurchaseDefault bool    `json:"is_purchase_default"`
		IsSalesDefault    bool    `json:"is_sales_default"`
	}
	if err := bindJSONCoerced(c, &body); err != nil {
		badRequest(c, "invalid JSON")
		return
	}
	row, err := a.Store.UpsertItemUOM(c.Request.Context(), itemID, store.ItemUOMInput{
		UOM:               body.UOM,
		Factor:            body.Factor,
		IsPurchaseDefault: body.IsPurchaseDefault,
		IsSalesDefault:    body.IsSalesDefault,
	})
	if err != nil {
		storeErr(c, err)
		return
	}
	created(c, row)
}

func (a *API) DeleteItemUOM(c *gin.Context) {
	id, okID := parsePathID(c)
	if !okID {
		return
	}
	if err := a.Store.DeleteItemUOM(c.Request.Context(), id); err != nil {
		storeErr(c, err)
		return
	}
	c.Status(http.StatusNoContent)
}

// ConvertUOM answers "how much is this in base units" without writing anything.
// A terminal uses it to show the operator what a quantity they just keyed in
// actually amounts to, before they commit to it.
func (a *API) ConvertUOM(c *gin.Context) {
	itemID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		badRequest(c, "invalid item id")
		return
	}
	var body struct {
		Qty float64 `json:"qty"`
		UOM string  `json:"uom"`
	}
	if err := bindJSONCoerced(c, &body); err != nil {
		badRequest(c, "invalid JSON")
		return
	}
	conv, err := a.Store.ConvertToBase(c.Request.Context(), itemID, body.Qty, body.UOM)
	if err != nil {
		storeErr(c, err)
		return
	}
	ok(c, gin.H{
		"entered_qty": conv.EnteredQty,
		"entered_uom": conv.EnteredUOM,
		"factor":      conv.Factor,
		"base_qty":    conv.BaseQty,
		"base_uom":    conv.BaseUOM,
	})
}
