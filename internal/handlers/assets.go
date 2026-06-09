package handlers

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"iag-warehouse/backend/internal/middleware"
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

func (a *API) SparePartsByAsset(c *gin.Context) {
	items, err := a.Store.ListSparePartsByAsset(c.Request.Context(), c.Param("asset_tag"))
	if err != nil {
		storeErr(c, err)
		return
	}
	ok(c, gin.H{"items": items})
}
