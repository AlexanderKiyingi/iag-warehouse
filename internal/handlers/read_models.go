package handlers

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"iag-warehouse/backend/internal/store"
)

func (a *API) GetReceipt(c *gin.Context) {
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		badRequest(c, "invalid receipt id")
		return
	}
	r, err := a.Store.GetReceipt(c.Request.Context(), id)
	if err != nil {
		storeErr(c, err)
		return
	}
	ok(c, r)
}

func (a *API) GetIssue(c *gin.Context) {
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		badRequest(c, "invalid issue id")
		return
	}
	iss, err := a.Store.GetIssue(c.Request.Context(), id)
	if err != nil {
		storeErr(c, err)
		return
	}
	ok(c, iss)
}

func (a *API) ListPickLists(c *gin.Context) {
	items, err := a.Store.ListPickLists(c.Request.Context(), c.Query("status"), 100)
	if err != nil {
		storeErr(c, err)
		return
	}
	ok(c, gin.H{"items": items})
}

func (a *API) GetPickList(c *gin.Context) {
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		badRequest(c, "invalid pick list id")
		return
	}
	pl, err := a.Store.GetPickList(c.Request.Context(), id)
	if err != nil {
		storeErr(c, err)
		return
	}
	ok(c, pl)
}

func (a *API) ListMovements(c *gin.Context) {
	var itemID *uuid.UUID
	if raw := c.Query("item_id"); raw != "" {
		id, err := uuid.Parse(raw)
		if err != nil {
			badRequest(c, "invalid item_id")
			return
		}
		itemID = &id
	}
	items, err := a.Store.ListMovements(c.Request.Context(), c.Query("movement_type"), itemID, 100)
	if err != nil {
		storeErr(c, err)
		return
	}
	ok(c, gin.H{"items": items})
}

func (a *API) PatchZone(c *gin.Context) {
	var body struct {
		Name     *string        `json:"name"`
		ZoneType *string        `json:"zone_type"`
		Attrs    map[string]any `json:"attrs"`
	}
	if err := c.ShouldBindJSON(&body); err != nil {
		badRequest(c, "invalid JSON")
		return
	}
	z, err := a.Store.UpdateZone(c.Request.Context(), c.Param("code"), body.Name, body.ZoneType, body.Attrs)
	if err != nil {
		storeErr(c, err)
		return
	}
	ok(c, z)
}

func (a *API) PatchBin(c *gin.Context) {
	var body struct {
		CapacityKg      *float64       `json:"capacity_kg"`
		TemperatureBand *string        `json:"temperature_band"`
		Status          *string        `json:"status"`
		Attrs           map[string]any `json:"attrs"`
	}
	if err := bindJSONCoerced(c, &body); err != nil {
		badRequest(c, "invalid JSON")
		return
	}
	b, err := a.Store.UpdateBin(c.Request.Context(), c.Param("code"), body.CapacityKg, body.TemperatureBand, body.Status, body.Attrs)
	if err != nil {
		storeErr(c, err)
		return
	}
	ok(c, b)
}

func (a *API) ListSpareCompat(c *gin.Context) {
	var itemID *uuid.UUID
	if raw := c.Query("item_id"); raw != "" {
		id, err := uuid.Parse(raw)
		if err != nil {
			badRequest(c, "invalid item_id")
			return
		}
		itemID = &id
	}
	items, err := a.Store.ListSpareCompat(c.Request.Context(), itemID)
	if err != nil {
		storeErr(c, err)
		return
	}
	ok(c, gin.H{"items": items})
}

func (a *API) CreateSpareCompat(c *gin.Context) {
	var body struct {
		ItemID    string `json:"item_id"`
		AssetType string `json:"asset_type"`
	}
	if err := c.ShouldBindJSON(&body); err != nil || body.ItemID == "" || body.AssetType == "" {
		badRequest(c, "item_id and asset_type are required")
		return
	}
	itemID, err := uuid.Parse(body.ItemID)
	if err != nil {
		badRequest(c, "invalid item_id")
		return
	}
	a.withIdempotency(c, func() (int, any) {
		row, err := a.Store.AddSpareCompat(c.Request.Context(), itemID, body.AssetType)
		if err != nil {
			if err == store.ErrNotFound {
				return http.StatusNotFound, gin.H{"error": "item not found"}
			}
			return http.StatusInternalServerError, gin.H{"error": err.Error()}
		}
		return http.StatusCreated, row
	})
}

func (a *API) DeleteSpareCompat(c *gin.Context) {
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		badRequest(c, "invalid id")
		return
	}
	if err := a.Store.DeleteSpareCompat(c.Request.Context(), id); err != nil {
		storeErr(c, err)
		return
	}
	c.Status(http.StatusNoContent)
}
