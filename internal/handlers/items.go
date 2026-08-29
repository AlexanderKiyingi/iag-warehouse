package handlers

import (
	"errors"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"iag-warehouse/backend/internal/models"
	"iag-warehouse/backend/internal/store"
)

func (a *API) ListItems(c *gin.Context) {
	// Exact-SKU lookup: services that key parts by SKU (e.g. iag-fleet
	// resolving a part to a warehouse item id) pass ?sku=. Returns a 0-or-1
	// element list so the caller can treat "unknown SKU" as an empty result
	// rather than a 404.
	if sku := strings.TrimSpace(c.Query("sku")); sku != "" {
		item, err := a.Store.GetItemBySKU(c.Request.Context(), sku)
		if err != nil {
			if errors.Is(err, store.ErrNotFound) {
				ok(c, gin.H{"items": []models.Item{}})
				return
			}
			storeErr(c, err)
			return
		}
		ok(c, gin.H{"items": []models.Item{item}})
		return
	}
	items, err := a.Store.ListItems(c.Request.Context(), c.Query("material_class"))
	if err != nil {
		storeErr(c, err)
		return
	}
	ok(c, gin.H{"items": items})
}

func (a *API) CreateItem(c *gin.Context) {
	var body struct {
		SKU           string         `json:"sku"`
		Name          string         `json:"name"`
		MaterialClass string         `json:"material_class"`
		TrackingMode  string         `json:"tracking_mode"`
		UOM           string         `json:"uom"`
		MinQty        float64        `json:"min_qty"`
		MaxQty        *float64       `json:"max_qty"`
		Attrs         map[string]any `json:"attrs"`
	}
	if err := bindJSONCoerced(c, &body); err != nil {
		badRequest(c, "invalid JSON")
		return
	}
	if body.SKU == "" || body.Name == "" {
		badRequest(c, "sku and name are required")
		return
	}
	// tracking_mode had no default, which made creation impossible for any
	// client without a field for it — and how an item is identified in the bin
	// is not a decision the person naming it is equipped to make. "bulk" is the
	// only safe default: it tracks neither lot nor serial, so it claims no
	// identity the warehouse cannot actually prove, and tightening it later is
	// a data-entry change rather than a data-correction one.
	//
	// material_class deliberately keeps no default. Its five values are a CHECK
	// constraint and a putaway-rule matching key, so inventing a sixth
	// "unclassified" bucket to paper over a missing form field would weaken a
	// live control. The field belongs on the form.
	if body.TrackingMode == "" {
		body.TrackingMode = models.TrackingBulk
	}
	if !validTrackingMode(body.TrackingMode) {
		badRequest(c, "tracking_mode must be one of: "+strings.Join(models.TrackingModes, ", "))
		return
	}
	if body.MaterialClass == "" {
		badRequest(c, "material_class is required, one of: "+strings.Join(models.MaterialClasses, ", "))
		return
	}
	if !validMaterialClass(body.MaterialClass) {
		badRequest(c, "material_class must be one of: "+strings.Join(models.MaterialClasses, ", "))
		return
	}
	if body.UOM == "" {
		body.UOM = "ea"
	}
	a.withIdempotency(c, func() (int, any) {
		item, err := a.Store.CreateItem(c.Request.Context(), body.SKU, body.Name, body.MaterialClass, body.TrackingMode, body.UOM, body.MinQty, body.MaxQty, body.Attrs)
		if err != nil {
			return http.StatusInternalServerError, gin.H{"error": err.Error()}
		}
		return http.StatusCreated, item
	})
}

func (a *API) GetItem(c *gin.Context) {
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		badRequest(c, "invalid item id")
		return
	}
	item, err := a.Store.GetItem(c.Request.Context(), id)
	if err != nil {
		storeErr(c, err)
		return
	}
	ok(c, item)
}

func (a *API) PatchItem(c *gin.Context) {
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		badRequest(c, "invalid item id")
		return
	}
	// SKU, UOM and MaterialClass were absent here, so a client that let someone
	// edit an item's code or unit sent those fields, got a 200 back, and watched
	// the value revert — the update had been silently discarded. They are
	// editable now. Status is deliberately still not: it moves through
	// PATCH /items/:id/status, behind its own permission, because retiring a
	// part is a different decision from correcting its description.
	var body struct {
		Name          *string        `json:"name"`
		SKU           *string        `json:"sku"`
		UOM           *string        `json:"uom"`
		MaterialClass *string        `json:"material_class"`
		TrackingMode  *string        `json:"tracking_mode"`
		MinQty        *float64       `json:"min_qty"`
		MaxQty        *float64       `json:"max_qty"`
		Attrs         map[string]any `json:"attrs"`
	}
	if err := bindJSONCoerced(c, &body); err != nil {
		badRequest(c, "invalid JSON")
		return
	}
	if body.MaterialClass != nil && !validMaterialClass(*body.MaterialClass) {
		badRequest(c, "material_class must be one of: "+strings.Join(models.MaterialClasses, ", "))
		return
	}
	// Retracking a stocked item would invalidate the lot and serial keys already
	// recorded against its balances, so it is refused while stock exists rather
	// than accepted and left to rot.
	if body.TrackingMode != nil {
		if !validTrackingMode(*body.TrackingMode) {
			badRequest(c, "tracking_mode must be one of: "+strings.Join(models.TrackingModes, ", "))
			return
		}
		balances, berr := a.Store.ListItemBalances(c.Request.Context(), id)
		if berr != nil {
			storeErr(c, berr)
			return
		}
		if len(balances) > 0 {
			badRequest(c, "tracking_mode cannot change while the item holds stock")
			return
		}
	}
	item, err := a.Store.UpdateItem(c.Request.Context(), id, store.UpdateItemInput{
		Name:          body.Name,
		SKU:           body.SKU,
		UOM:           body.UOM,
		MaterialClass: body.MaterialClass,
		TrackingMode:  body.TrackingMode,
		MinQty:        body.MinQty,
		MaxQty:        body.MaxQty,
		Attrs:         body.Attrs,
	})
	if err != nil {
		// A duplicate SKU is the caller renaming an item onto one that exists,
		// which is a conflict they can resolve — not a 500.
		if errors.Is(err, store.ErrDuplicateSKU) {
			c.JSON(http.StatusConflict, gin.H{"error": "another item already uses that sku"})
			return
		}
		storeErr(c, err)
		return
	}
	ok(c, item)
}

// ItemStockSummary answers "how much of each item is on hand" in one call.
//
// GET /items/:id/balances is per item and bin-by-bin. A list screen has a
// closing-quantity column and a hundred rows, so filling it from that endpoint
// costs a hundred round trips — which is why, in practice, clients left the
// column blank and an inventory system displayed no inventory. Optional
// ?facility_code= narrows to one site.
func (a *API) ItemStockSummary(c *gin.Context) {
	rows, err := a.Store.ListStockSummary(c.Request.Context(), c.Query("facility_code"))
	if err != nil {
		storeErr(c, err)
		return
	}
	ok(c, gin.H{"items": rows})
}

func validMaterialClass(s string) bool {
	for _, v := range models.MaterialClasses {
		if v == s {
			return true
		}
	}
	return false
}

func validTrackingMode(s string) bool {
	for _, v := range models.TrackingModes {
		if v == s {
			return true
		}
	}
	return false
}

func (a *API) ItemBalances(c *gin.Context) {
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		badRequest(c, "invalid item id")
		return
	}
	balances, err := a.Store.ListItemBalances(c.Request.Context(), id)
	if err != nil {
		storeErr(c, err)
		return
	}
	ok(c, gin.H{"items": balances})
}

func (a *API) LowStock(c *gin.Context) {
	items, err := a.Store.ListLowStock(c.Request.Context())
	if err != nil {
		storeErr(c, err)
		return
	}
	ok(c, gin.H{"items": items})
}
