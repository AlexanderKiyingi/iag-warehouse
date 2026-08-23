package handlers

import (
	"net/http"
	"strings"

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

// errNoIssueReference is the refusal text for an unreferenced issue. It names
// all three acceptable fields rather than saying "reference required", because
// a caller reading it is one field away from a valid request and should not
// have to read the schema to find out which.
const errNoIssueReference = "an issue needs a reason: supply cost_center, production_order_ref or work_order_ref " +
	"(department records who took the stock, not what consumed it)"

// hasIssueReference reports whether an issue names the document that caused it.
//
// Department is deliberately not accepted. It answers "who", and an issue that
// can only answer "who" cannot be costed to an order, reconciled against a
// backflush, or defended in a variance review — which is the whole reason the
// reference is mandatory.
func hasIssueReference(in store.CreateIssueInput) bool {
	for _, ref := range []*string{in.CostCenter, in.ProductionOrderRef, in.WorkOrderRef} {
		if ref != nil && strings.TrimSpace(*ref) != "" {
			return true
		}
	}
	return false
}

func (a *API) CreateIssue(c *gin.Context) {
	in, createdBy, err := bindIssueInput(c)
	if err != nil {
		badRequest(c, err.Error())
		return
	}
	if a.Cfg.IssueRequireReference && !hasIssueReference(in) {
		badRequest(c, errNoIssueReference)
		return
	}
	in.CreatedBy = createdBy
	a.withIdempotency(c, func() (int, any) {
		iss, err := a.Store.CreateIssue(c.Request.Context(), in)
		if err != nil {
			return statusForStoreErr(err), gin.H{"error": messageForStoreErr(err)}
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
	// This path creates and posts in one call, so an unreferenced issue here
	// reaches the ledger immediately rather than sitting in draft where somebody
	// might notice. FR-ISS-14 allows a consumables issue straight to a cost
	// centre — the cost centre is the reference, and it still has to be named.
	if a.Cfg.IssueRequireReference && !hasIssueReference(in) {
		badRequest(c, errNoIssueReference)
		return
	}
	in.CreatedBy = createdBy
	a.withIdempotency(c, func() (int, any) {
		iss, err := a.Store.CreateIssue(c.Request.Context(), in)
		if err != nil {
			return statusForStoreErr(err), gin.H{"error": messageForStoreErr(err)}
		}
		iss, err = a.Store.PostIssue(c.Request.Context(), iss.ID, createdBy)
		if err != nil {
			return statusForStoreErr(err), gin.H{"error": messageForStoreErr(err)}
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
			return statusForStoreErr(err), gin.H{"error": messageForStoreErr(err)}
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
		RequestedBy        string `json:"requested_by"`
		Priority           string `json:"priority"`
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
	if err := bindJSONCoerced(c, &body); err != nil {
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
		RequestedBy:        strPtr(body.RequestedBy),
		Priority:           strPtr(body.Priority),
		BatchBusinessID:    strPtr(body.BatchBusinessID),
		Notes:              strPtr(body.Notes),
		Lines:              lines,
		// Resolved here rather than in the store: whether this caller may move a
		// restricted item is a fact about the request, and the store has no
		// claims to ask.
		AllowRestrictedItems: callerHolds(c, PermOverrideItemStatus),
	}, createdBy, nil
}
