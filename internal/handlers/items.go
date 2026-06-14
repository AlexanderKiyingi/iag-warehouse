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
	if err := c.ShouldBindJSON(&body); err != nil || body.SKU == "" || body.Name == "" || body.MaterialClass == "" || body.TrackingMode == "" {
		badRequest(c, "sku, name, material_class, and tracking_mode are required")
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
	var body struct {
		Name   *string        `json:"name"`
		MinQty *float64       `json:"min_qty"`
		MaxQty *float64       `json:"max_qty"`
		Attrs  map[string]any `json:"attrs"`
	}
	if err := c.ShouldBindJSON(&body); err != nil {
		badRequest(c, "invalid JSON")
		return
	}
	item, err := a.Store.UpdateItem(c.Request.Context(), id, body.Name, body.MinQty, body.MaxQty, body.Attrs)
	if err != nil {
		storeErr(c, err)
		return
	}
	ok(c, item)
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
