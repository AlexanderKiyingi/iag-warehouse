package handlers

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"iag-warehouse/backend/internal/middleware"
	"iag-warehouse/backend/internal/store"
)

func (a *API) ListIssues(c *gin.Context) {
	items, err := a.Store.ListIssues(c.Request.Context(), c.Query("status"), 100)
	if err != nil {
		storeErr(c, err)
		return
	}
	ok(c, gin.H{"items": items})
}

func (a *API) CreateIssue(c *gin.Context) {
	in, createdBy, err := bindIssueInput(c)
	if err != nil {
		badRequest(c, err.Error())
		return
	}
	in.CreatedBy = createdBy
	a.withIdempotency(c, func() (int, any) {
		iss, err := a.Store.CreateIssue(c.Request.Context(), in)
		if err != nil {
			return http.StatusInternalServerError, gin.H{"error": err.Error()}
		}
		return http.StatusCreated, iss
	})
}

func (a *API) IssueForDepartment(c *gin.Context) {
	in, createdBy, err := bindIssueInput(c)
	if err != nil {
		badRequest(c, err.Error())
		return
	}
	if in.Department == nil || *in.Department == "" {
		badRequest(c, "department is required")
		return
	}
	in.CreatedBy = createdBy
	a.withIdempotency(c, func() (int, any) {
		iss, err := a.Store.CreateIssue(c.Request.Context(), in)
		if err != nil {
			return http.StatusInternalServerError, gin.H{"error": err.Error()}
		}
		iss, err = a.Store.PostIssue(c.Request.Context(), iss.ID, createdBy)
		if err != nil {
			return http.StatusInternalServerError, gin.H{"error": err.Error()}
		}
		return http.StatusCreated, iss
	})
}

func (a *API) PostIssue(c *gin.Context) {
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		badRequest(c, "invalid issue id")
		return
	}
	var actorID *uuid.UUID
	if uid, ok := middleware.UserID(c); ok {
		actorID = &uid
	}
	a.withIdempotency(c, func() (int, any) {
		iss, err := a.Store.PostIssue(c.Request.Context(), id, actorID)
		if err != nil {
			if err == store.ErrInsufficientStock {
				return http.StatusUnprocessableEntity, gin.H{"error": "insufficient stock"}
			}
			return http.StatusInternalServerError, gin.H{"error": err.Error()}
		}
		return http.StatusOK, iss
	})
}

func bindIssueInput(c *gin.Context) (store.CreateIssueInput, *uuid.UUID, error) {
	var body struct {
		Department         string `json:"department"`
		CostCenter         string `json:"cost_center"`
		ProductionOrderRef string `json:"production_order_ref"`
		WorkOrderRef       string `json:"work_order_ref"`
		BatchBusinessID    string `json:"batch_business_id"`
		Notes              string `json:"notes"`
		Lines              []struct {
			ItemID    string  `json:"item_id"`
			Qty       float64 `json:"qty"`
			UOM       string  `json:"uom"`
			BinCode   string  `json:"bin_code"`
			LotKey    string  `json:"lot_key"`
			SerialKey string  `json:"serial_key"`
		} `json:"lines"`
	}
	if err := c.ShouldBindJSON(&body); err != nil {
		return store.CreateIssueInput{}, nil, err
	}
	var lines []store.IssueLineInput
	for _, l := range body.Lines {
		itemID, err := uuid.Parse(l.ItemID)
		if err != nil {
			return store.CreateIssueInput{}, nil, err
		}
		uom := l.UOM
		if uom == "" {
			uom = "ea"
		}
		lines = append(lines, store.IssueLineInput{
			ItemID: itemID, Qty: l.Qty, UOM: uom, BinCode: l.BinCode, LotKey: l.LotKey, SerialKey: l.SerialKey,
		})
	}
	var createdBy *uuid.UUID
	if uid, ok := middleware.UserID(c); ok {
		createdBy = &uid
	}
	return store.CreateIssueInput{
		Department:         strPtr(body.Department),
		CostCenter:         strPtr(body.CostCenter),
		ProductionOrderRef: strPtr(body.ProductionOrderRef),
		WorkOrderRef:       strPtr(body.WorkOrderRef),
		BatchBusinessID:    strPtr(body.BatchBusinessID),
		Notes:              strPtr(body.Notes),
		Lines:              lines,
	}, createdBy, nil
}
