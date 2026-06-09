package handlers

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"iag-warehouse/backend/internal/middleware"
	"iag-warehouse/backend/internal/store"
)

func (a *API) ProductionConsume(c *gin.Context) {
	var body struct {
		BatchBusinessID string `json:"batch_business_id"`
		FacilityCode    string `json:"facility_code"`
		Lines           []struct {
			ItemID  string  `json:"item_id"`
			Qty     float64 `json:"qty"`
			BinCode string  `json:"bin_code"`
			LotKey  string  `json:"lot_key"`
		} `json:"lines"`
	}
	if err := c.ShouldBindJSON(&body); err != nil || body.BatchBusinessID == "" {
		badRequest(c, "batch_business_id and lines are required")
		return
	}
	var lines []store.ProductionConsumeLine
	for _, l := range body.Lines {
		itemID, err := uuid.Parse(l.ItemID)
		if err != nil {
			badRequest(c, "invalid item_id")
			return
		}
		lines = append(lines, store.ProductionConsumeLine{
			ItemID: itemID, Qty: l.Qty, BinCode: l.BinCode, LotKey: l.LotKey,
		})
	}
	var actorID *uuid.UUID
	if uid, ok := middleware.UserID(c); ok {
		actorID = &uid
	}
	a.withIdempotency(c, func() (int, any) {
		result, err := a.Store.ProductionConsume(c.Request.Context(), store.ProductionConsumeInput{
			BatchBusinessID: body.BatchBusinessID,
			FacilityCode:    body.FacilityCode,
			Lines:           lines,
			ActorID:         actorID,
		})
		if err != nil {
			if err == store.ErrInsufficientStock {
				return http.StatusUnprocessableEntity, gin.H{"error": "insufficient stock"}
			}
			return http.StatusInternalServerError, gin.H{"error": err.Error()}
		}
		return http.StatusOK, result
	})
}

func (a *API) ProductionOutput(c *gin.Context) {
	var body struct {
		BatchBusinessID string  `json:"batch_business_id"`
		SKU             string  `json:"sku"`
		ItemID          string  `json:"item_id"`
		Qty             float64 `json:"qty"`
		BinCode         string  `json:"bin_code"`
		LotKey          string  `json:"lot_key"`
		QCHold          bool    `json:"qc_hold"`
	}
	if err := c.ShouldBindJSON(&body); err != nil || body.BatchBusinessID == "" || body.BinCode == "" {
		badRequest(c, "batch_business_id, item_id, qty, and bin_code are required")
		return
	}
	itemID, err := uuid.Parse(body.ItemID)
	if err != nil {
		badRequest(c, "invalid item_id")
		return
	}
	if !body.QCHold {
		body.QCHold = true
	}
	var actorID *uuid.UUID
	if uid, ok := middleware.UserID(c); ok {
		actorID = &uid
	}
	a.withIdempotency(c, func() (int, any) {
		result, err := a.Store.ProductionOutput(c.Request.Context(), store.ProductionOutputInput{
			BatchBusinessID: body.BatchBusinessID,
			SKU:             body.SKU,
			ItemID:          itemID,
			Qty:             body.Qty,
			BinCode:         body.BinCode,
			LotKey:          body.LotKey,
			QCHold:          body.QCHold,
			ActorID:         actorID,
		})
		if err != nil {
			return http.StatusInternalServerError, gin.H{"error": err.Error()}
		}
		return http.StatusOK, result
	})
}
