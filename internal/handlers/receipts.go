package handlers

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"iag-warehouse/backend/internal/middleware"
	"iag-warehouse/backend/internal/store"
)

func (a *API) ListReceipts(c *gin.Context) {
	items, err := a.Store.ListReceipts(c.Request.Context(), c.Query("status"), 100)
	if err != nil {
		storeErr(c, err)
		return
	}
	ok(c, gin.H{"items": items})
}

func (a *API) CreateReceipt(c *gin.Context) {
	var body struct {
		ReceiptType string `json:"receipt_type"`
		SourceRef   string `json:"source_ref"`
		GRNID       string `json:"grn_id"`
		POID        string `json:"po_id"`
		Notes       string `json:"notes"`
		Lines       []receiptLineBody `json:"lines"`
	}
	if err := bindJSONCoerced(c, &body); err != nil {
		badRequest(c, "invalid JSON")
		return
	}
	if body.ReceiptType == "" {
		body.ReceiptType = "standard"
	}
	lines, err := receiptLinesFromBody(body.Lines)
	if err != nil {
		badRequest(c, err.Error())
		return
	}
	var createdBy *uuid.UUID
	if uid, ok := middleware.UserID(c); ok {
		createdBy = &uid
	}
	a.withIdempotency(c, func() (int, any) {
		r, err := a.Store.CreateReceipt(c.Request.Context(), store.CreateReceiptInput{
			ReceiptType: body.ReceiptType,
			SourceRef:   strPtr(body.SourceRef),
			GRNID:       strPtr(body.GRNID),
			POID:        strPtr(body.POID),
			Notes:       strPtr(body.Notes),
			Lines:       lines,
			CreatedBy:   createdBy,
		})
		if err != nil {
			return http.StatusInternalServerError, gin.H{"error": err.Error()}
		}
		return http.StatusCreated, r
	})
}

func (a *API) CreateReceiptFromGRN(c *gin.Context) {
	var body struct {
		GRNID string            `json:"grn_id"`
		POID  string            `json:"po_id"`
		Lines []receiptLineBody `json:"lines"`
	}
	if err := bindJSONCoerced(c, &body); err != nil || body.GRNID == "" {
		badRequest(c, "grn_id and lines are required")
		return
	}
	lines, err := receiptLinesFromBody(body.Lines)
	if err != nil {
		badRequest(c, err.Error())
		return
	}
	var createdBy *uuid.UUID
	if uid, ok := middleware.UserID(c); ok {
		createdBy = &uid
	}
	a.withIdempotency(c, func() (int, any) {
		r, err := a.Store.CreateReceiptFromGRN(c.Request.Context(), body.GRNID, body.POID, lines, createdBy)
		if err != nil {
			return http.StatusInternalServerError, gin.H{"error": err.Error()}
		}
		return http.StatusCreated, r
	})
}

func (a *API) PostReceipt(c *gin.Context) {
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		badRequest(c, "invalid receipt id")
		return
	}
	var actorID *uuid.UUID
	if uid, ok := middleware.UserID(c); ok {
		actorID = &uid
	}
	a.withIdempotency(c, func() (int, any) {
		r, err := a.Store.PostReceipt(c.Request.Context(), id, actorID)
		if err != nil {
			if err == store.ErrNotFound {
				return http.StatusNotFound, gin.H{"error": "not found"}
			}
			if err == store.ErrInsufficientStock {
				return http.StatusUnprocessableEntity, gin.H{"error": "insufficient stock"}
			}
			return http.StatusInternalServerError, gin.H{"error": err.Error()}
		}
		return http.StatusOK, r
	})
}

type receiptLineBody struct {
	ItemID          string  `json:"item_id"`
	Qty             float64 `json:"qty"`
	UOM             string  `json:"uom"`
	BinCode         string  `json:"bin_code"`
	LotKey          string  `json:"lot_key"`
	BatchBusinessID string  `json:"batch_business_id"`
}

func receiptLinesFromBody(lines []receiptLineBody) ([]store.ReceiptLineInput, error) {
	var out []store.ReceiptLineInput
	for _, l := range lines {
		itemID, err := uuid.Parse(l.ItemID)
		if err != nil {
			return nil, err
		}
		uom := l.UOM
		if uom == "" {
			uom = "ea"
		}
		out = append(out, store.ReceiptLineInput{
			ItemID: itemID, Qty: l.Qty, UOM: uom, BinCode: l.BinCode,
			LotKey: l.LotKey, BatchBusinessID: strPtr(l.BatchBusinessID),
		})
	}
	return out, nil
}

func strPtr(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}
