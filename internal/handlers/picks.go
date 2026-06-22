package handlers

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"iag-warehouse/backend/internal/middleware"
	"iag-warehouse/backend/internal/store"
)

func (a *API) CreatePickList(c *gin.Context) {
	var body struct {
		OrderRef string `json:"order_ref"`
		Notes    string `json:"notes"`
		Lines    []struct {
			ItemID  string  `json:"item_id"`
			Qty     float64 `json:"qty"`
			BinCode string  `json:"bin_code"`
			LotKey  string  `json:"lot_key"`
		} `json:"lines"`
	}
	if err := bindJSONCoerced(c, &body); err != nil || len(body.Lines) == 0 {
		badRequest(c, "lines are required")
		return
	}
	var lines []store.PickLineInput
	for _, l := range body.Lines {
		itemID, err := uuid.Parse(l.ItemID)
		if err != nil {
			badRequest(c, "invalid item_id")
			return
		}
		lines = append(lines, store.PickLineInput{ItemID: itemID, Qty: l.Qty, BinCode: l.BinCode, LotKey: l.LotKey})
	}
	var createdBy *uuid.UUID
	if uid, ok := middleware.UserID(c); ok {
		createdBy = &uid
	}
	a.withIdempotency(c, func() (int, any) {
		pl, err := a.Store.CreatePickList(c.Request.Context(), store.CreatePickListInput{
			OrderRef: strPtr(body.OrderRef), Notes: strPtr(body.Notes), Lines: lines, CreatedBy: createdBy,
		})
		if err != nil {
			return http.StatusInternalServerError, gin.H{"error": err.Error()}
		}
		return http.StatusCreated, pl
	})
}

func (a *API) ConfirmPickList(c *gin.Context) {
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		badRequest(c, "invalid pick list id")
		return
	}
	var actorID *uuid.UUID
	if uid, ok := middleware.UserID(c); ok {
		actorID = &uid
	}
	a.withIdempotency(c, func() (int, any) {
		pl, err := a.Store.ConfirmPickList(c.Request.Context(), id, actorID)
		if err != nil {
			if err == store.ErrInsufficientStock {
				return http.StatusUnprocessableEntity, gin.H{"error": "insufficient stock"}
			}
			if err == store.ErrStockNotAvailable {
				return http.StatusUnprocessableEntity, gin.H{"error": "stock on QC hold or damaged"}
			}
			return http.StatusInternalServerError, gin.H{"error": err.Error()}
		}
		return http.StatusOK, pl
	})
}

func (a *API) CancelPickList(c *gin.Context) {
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		badRequest(c, "invalid pick list id")
		return
	}
	var actorID *uuid.UUID
	if uid, ok := middleware.UserID(c); ok {
		actorID = &uid
	}
	a.withIdempotency(c, func() (int, any) {
		pl, err := a.Store.CancelPickList(c.Request.Context(), id, actorID)
		if err != nil {
			switch err {
			case store.ErrNotFound:
				return http.StatusNotFound, gin.H{"error": "pick list not found"}
			case store.ErrConflict:
				return http.StatusConflict, gin.H{"error": "a confirmed pick list cannot be cancelled"}
			default:
				return http.StatusInternalServerError, gin.H{"error": err.Error()}
			}
		}
		return http.StatusOK, pl
	})
}

func (a *API) CreatePackSession(c *gin.Context) {
	var body struct {
		PickListID string         `json:"pick_list_id"`
		Attrs      map[string]any `json:"attrs"`
	}
	_ = c.ShouldBindJSON(&body)
	var pickID *uuid.UUID
	if body.PickListID != "" {
		id, err := uuid.Parse(body.PickListID)
		if err != nil {
			badRequest(c, "invalid pick_list_id")
			return
		}
		pickID = &id
	}
	var createdBy *uuid.UUID
	if uid, ok := middleware.UserID(c); ok {
		createdBy = &uid
	}
	a.withIdempotency(c, func() (int, any) {
		id, err := a.Store.CreatePackSession(c.Request.Context(), pickID, createdBy, body.Attrs)
		if err != nil {
			return http.StatusInternalServerError, gin.H{"error": err.Error()}
		}
		return http.StatusCreated, gin.H{"id": id}
	})
}
