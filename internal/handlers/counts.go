package handlers

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"iag-warehouse/backend/internal/middleware"
	"iag-warehouse/backend/internal/store"
)

// Controlled cycle counting.

func (a *API) ListCountTasks(c *gin.Context) {
	items, err := a.Store.ListCountTasks(c.Request.Context(), c.Query("status"), 100)
	if err != nil {
		storeErr(c, err)
		return
	}
	ok(c, gin.H{"items": items})
}

func (a *API) GetCountTask(c *gin.Context) {
	id, okID := parsePathID(c)
	if !okID {
		return
	}
	task, err := a.Store.GetCountTask(c.Request.Context(), id)
	if err != nil {
		storeErr(c, err)
		return
	}
	ok(c, task)
}

func (a *API) CreateCountTask(c *gin.Context) {
	var body struct {
		ScopeType      string  `json:"scope_type"`
		ScopeRef       string  `json:"scope_ref"`
		Blind          *bool   `json:"blind"`
		TolerancePct   float64 `json:"tolerance_pct"`
		ToleranceValue float64 `json:"tolerance_value"`
		DueOnly        bool    `json:"due_only"`
		Notes          string  `json:"notes"`
	}
	if err := bindJSONCoerced(c, &body); err != nil {
		badRequest(c, "invalid JSON")
		return
	}
	in := store.CountTaskInput{
		ScopeType:      body.ScopeType,
		ScopeRef:       body.ScopeRef,
		Blind:          body.Blind == nil || *body.Blind, // blind unless asked otherwise
		TolerancePct:   body.TolerancePct,
		ToleranceValue: body.ToleranceValue,
		DueOnly:        body.DueOnly,
		Notes:          strPtr(body.Notes),
	}
	if uid, okUID := middleware.UserID(c); okUID {
		in.CreatedBy = &uid
	}
	a.withIdempotency(c, func() (int, any) {
		task, err := a.Store.CreateCountTask(c.Request.Context(), in)
		if err != nil {
			return statusForStoreErr(err), gin.H{"error": messageForStoreErr(err)}
		}
		return http.StatusCreated, task
	})
}

// RecordCount is the endpoint an RF terminal hits once per position counted.
func (a *API) RecordCount(c *gin.Context) {
	taskID, lineID, okIDs := parseTaskAndLineID(c)
	if !okIDs {
		return
	}
	var body struct {
		CountedQty float64 `json:"counted_qty"`
		UOM        string  `json:"uom"`
		Note       string  `json:"note"`
	}
	if err := bindJSONCoerced(c, &body); err != nil {
		badRequest(c, "invalid JSON")
		return
	}
	var actorID *uuid.UUID
	if uid, okUID := middleware.UserID(c); okUID {
		actorID = &uid
	}

	countedQty := body.CountedQty
	// A counter may report in whatever unit is written on the sack; the sheet is
	// kept in base units like everything else.
	if body.UOM != "" {
		line, err := a.Store.GetCountLine(c.Request.Context(), lineID)
		if err != nil {
			storeErr(c, err)
			return
		}
		conv, cerr := a.Store.ConvertToBase(c.Request.Context(), line.ItemID, body.CountedQty, body.UOM)
		if cerr != nil {
			storeErr(c, cerr)
			return
		}
		countedQty = conv.BaseQty
	}

	line, err := a.Store.RecordCount(c.Request.Context(), taskID, lineID, countedQty, strPtr(body.Note), actorID)
	if err != nil {
		storeErr(c, err)
		return
	}
	ok(c, line)
}

// AddCountLine records stock found in a place the system did not expect it.
func (a *API) AddCountLine(c *gin.Context) {
	taskID, okID := parsePathID(c)
	if !okID {
		return
	}
	var body struct {
		ItemID    string `json:"item_id"`
		ItemSKU   string `json:"item_sku"`
		BinCode   string `json:"bin_code"`
		LotKey    string `json:"lot_key"`
		SerialKey string `json:"serial_key"`
	}
	if err := bindJSONCoerced(c, &body); err != nil {
		badRequest(c, "invalid JSON")
		return
	}
	itemID, err := a.resolveItemID(c.Request.Context(), body.ItemID, body.ItemSKU)
	if err != nil {
		badRequest(c, "item_id or item_sku is required")
		return
	}
	line, err := a.Store.AddCountLine(c.Request.Context(), taskID, itemID, body.BinCode, body.LotKey, body.SerialKey)
	if err != nil {
		storeErr(c, err)
		return
	}
	created(c, line)
}

func (a *API) SubmitCountTask(c *gin.Context) {
	id, okID := parsePathID(c)
	if !okID {
		return
	}
	var actorID *uuid.UUID
	if uid, okUID := middleware.UserID(c); okUID {
		actorID = &uid
	}
	task, err := a.Store.SubmitCountTask(c.Request.Context(), id, actorID)
	if err != nil {
		storeErr(c, err)
		return
	}
	ok(c, task)
}

// SetCountLineStatus is the reviewer's verdict on a single variance.
func (a *API) SetCountLineStatus(c *gin.Context) {
	taskID, lineID, okIDs := parseTaskAndLineID(c)
	if !okIDs {
		return
	}
	var body struct {
		Status string `json:"status"`
		Note   string `json:"note"`
	}
	if err := bindJSONCoerced(c, &body); err != nil {
		badRequest(c, "invalid JSON")
		return
	}
	line, err := a.Store.SetCountLineStatus(c.Request.Context(), taskID, lineID, body.Status, strPtr(body.Note))
	if err != nil {
		storeErr(c, err)
		return
	}
	ok(c, line)
}

func (a *API) ReopenCountTask(c *gin.Context) {
	id, okID := parsePathID(c)
	if !okID {
		return
	}
	task, err := a.Store.ReopenCountTask(c.Request.Context(), id)
	if err != nil {
		storeErr(c, err)
		return
	}
	ok(c, task)
}

// ApproveCountTask posts the accepted variances. The separate-approver rule is
// applied here from configuration rather than being hard-coded, because a
// single-operator depot genuinely cannot satisfy it and would otherwise be
// unable to count at all.
func (a *API) ApproveCountTask(c *gin.Context) {
	id, okID := parsePathID(c)
	if !okID {
		return
	}
	var actorID *uuid.UUID
	if uid, okUID := middleware.UserID(c); okUID {
		actorID = &uid
	}
	task, err := a.Store.ApproveCountTask(c.Request.Context(), id, actorID, a.Cfg.CountRequireSeparateApprover)
	if err != nil {
		storeErr(c, err)
		return
	}
	ok(c, task)
}

func (a *API) CancelCountTask(c *gin.Context) {
	id, okID := parsePathID(c)
	if !okID {
		return
	}
	task, err := a.Store.CancelCountTask(c.Request.Context(), id)
	if err != nil {
		storeErr(c, err)
		return
	}
	ok(c, task)
}

// RunABCClassification re-ranks the catalogue and puts each item on a counting
// interval appropriate to what it is worth.
func (a *API) RunABCClassification(c *gin.Context) {
	var body struct {
		Days      int            `json:"days"`
		APct      float64        `json:"a_pct"`
		BPct      float64        `json:"b_pct"`
		Intervals map[string]int `json:"intervals"`
	}
	_ = bindJSONCoerced(c, &body)

	results, basis, err := a.Store.RunABCClassification(c.Request.Context(), store.ABCInput{
		Days: body.Days, APct: body.APct, BPct: body.BPct, Intervals: body.Intervals,
	})
	if err != nil {
		storeErr(c, err)
		return
	}
	counts := map[string]int{"A": 0, "B": 0, "C": 0}
	for _, r := range results {
		counts[r.Class]++
	}
	ok(c, gin.H{"items": results, "basis": basis, "classified": len(results), "counts": counts})
}

func parseTaskAndLineID(c *gin.Context) (uuid.UUID, uuid.UUID, bool) {
	taskID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		badRequest(c, "invalid id")
		return uuid.Nil, uuid.Nil, false
	}
	lineID, err := uuid.Parse(c.Param("lineId"))
	if err != nil {
		badRequest(c, "invalid line id")
		return uuid.Nil, uuid.Nil, false
	}
	return taskID, lineID, true
}
