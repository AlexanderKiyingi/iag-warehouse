package handlers

import (
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"iag-warehouse/backend/internal/events"
	"iag-warehouse/backend/internal/middleware"
	"iag-warehouse/backend/internal/models"
	"iag-warehouse/backend/internal/slips"
	"iag-warehouse/backend/internal/store"
)

// Equipment handover slips and cargo gate passes.

func (a *API) ListSlips(c *gin.Context) {
	items, err := a.Store.ListSlips(c.Request.Context(), store.SlipFilter{
		SlipType: c.Query("slip_type"),
		Status:   c.Query("status"),
		Overdue:  c.Query("overdue") == "true",
	})
	if err != nil {
		storeErr(c, err)
		return
	}
	ok(c, gin.H{"items": items})
}

func (a *API) GetSlip(c *gin.Context) {
	id, okID := parsePathID(c)
	if !okID {
		return
	}
	slip, err := a.Store.GetSlip(c.Request.Context(), id)
	if err != nil {
		storeErr(c, err)
		return
	}
	ok(c, slip)
}

type slipLineBody struct {
	ItemID       string  `json:"item_id"`
	ItemSKU      string  `json:"item_sku"`
	AssetTag     string  `json:"asset_tag"`
	Description  string  `json:"description"`
	Qty          float64 `json:"qty"`
	UOM          string  `json:"uom"`
	SerialNo     string  `json:"serial_no"`
	LotKey       string  `json:"lot_key"`
	ConditionOut string  `json:"condition_out"`
}

// CreateSlip raises a draft handover slip or cargo gate pass.
func (a *API) CreateSlip(c *gin.Context) {
	var body struct {
		SlipType     string `json:"slip_type"`
		Purpose      string `json:"purpose"`
		Notes        string `json:"notes"`
		FacilityCode string `json:"facility_code"`

		IssuedToName  string `json:"issued_to_name"`
		Dept          string `json:"dept"`
		FromCustodian string `json:"from_custodian"`
		ToCustodian   string `json:"to_custodian"`

		DriverName  string `json:"driver_name"`
		DriverIDNo  string `json:"driver_id_no"`
		VehicleReg  string `json:"vehicle_reg"`
		Transporter string `json:"transporter"`
		Destination string `json:"destination"`

		Returnable bool   `json:"returnable"`
		ReturnBy   string `json:"return_by"`

		IssueID    string `json:"issue_id"`
		PickListID string `json:"pick_list_id"`
		LPN        string `json:"lpn"`
		RefType    string `json:"ref_type"`
		RefID      string `json:"ref_id"`

		Lines []slipLineBody `json:"lines"`
	}
	if err := bindJSONCoerced(c, &body); err != nil {
		badRequest(c, "invalid JSON")
		return
	}
	ctx := c.Request.Context()

	in := store.CreateSlipInput{
		SlipType:      body.SlipType,
		Purpose:       body.Purpose,
		Notes:         body.Notes,
		IssuedToName:  body.IssuedToName,
		Dept:          body.Dept,
		FromCustodian: body.FromCustodian,
		ToCustodian:   body.ToCustodian,
		DriverName:    body.DriverName,
		DriverIDNo:    body.DriverIDNo,
		VehicleReg:    body.VehicleReg,
		Transporter:   body.Transporter,
		Destination:   body.Destination,
		Returnable:    body.Returnable,
		RefType:       body.RefType,
		RefID:         body.RefID,
		RequestedName: middleware.ActorName(c),
	}
	if uid, okUID := middleware.UserID(c); okUID {
		in.CreatedBy = &uid
		in.RequestedBy = &uid
	}
	if s := strings.TrimSpace(body.ReturnBy); s != "" {
		d, err := time.Parse("2006-01-02", s)
		if err != nil {
			badRequest(c, "return_by must be YYYY-MM-DD")
			return
		}
		in.ReturnBy = &d
	}
	if s := strings.TrimSpace(body.FacilityCode); s != "" {
		f, err := a.Store.GetFacilityByCode(ctx, s)
		if err != nil {
			badRequest(c, "unknown facility_code")
			return
		}
		in.FacilityID = &f.ID
	}
	if s := strings.TrimSpace(body.IssueID); s != "" {
		id, err := uuid.Parse(s)
		if err != nil {
			badRequest(c, "invalid issue_id")
			return
		}
		in.IssueID = &id
	}
	if s := strings.TrimSpace(body.PickListID); s != "" {
		id, err := uuid.Parse(s)
		if err != nil {
			badRequest(c, "invalid pick_list_id")
			return
		}
		in.PickListID = &id
	}
	if s := strings.TrimSpace(body.LPN); s != "" {
		hu, err := a.Store.GetHandlingUnitByLPN(ctx, s)
		if err != nil {
			badRequest(c, "unknown lpn")
			return
		}
		in.HUID = &hu.ID
	}

	for _, l := range body.Lines {
		line := store.SlipLineInput{
			Description:  l.Description,
			Qty:          l.Qty,
			UOM:          l.UOM,
			SerialNo:     l.SerialNo,
			LotKey:       l.LotKey,
			ConditionOut: l.ConditionOut,
		}
		if s := strings.TrimSpace(l.AssetTag); s != "" {
			asset, err := a.Store.GetAssetByTag(ctx, s)
			if err != nil {
				badRequest(c, "unknown asset_tag "+s)
				return
			}
			line.AssetID = &asset.ID
			if line.Description == "" {
				line.Description = asset.AssetTag
			}
			if line.SerialNo == "" && asset.SerialNo != nil {
				line.SerialNo = *asset.SerialNo
			}
		}
		if strings.TrimSpace(l.ItemID) != "" || strings.TrimSpace(l.ItemSKU) != "" {
			itemID, err := a.resolveItemID(ctx, l.ItemID, l.ItemSKU)
			if err != nil {
				badRequest(c, "unknown item on a slip line")
				return
			}
			line.ItemID = &itemID
			if line.Description == "" {
				if item, ierr := a.Store.GetItem(ctx, itemID); ierr == nil {
					line.Description = item.Name
				}
			}
		}
		in.Lines = append(in.Lines, line)
	}

	a.withIdempotency(c, func() (int, any) {
		slip, err := a.Store.CreateSlip(ctx, in)
		if err != nil {
			return statusForStoreErr(err), gin.H{"error": messageForStoreErr(err)}
		}
		return http.StatusCreated, slip
	})
}

// AuthorizeSlip signs a draft off, giving it its number and gate token.
func (a *API) AuthorizeSlip(c *gin.Context) {
	id, okID := parsePathID(c)
	if !okID {
		return
	}
	var actorID *uuid.UUID
	if uid, okUID := middleware.UserID(c); okUID {
		actorID = &uid
	}
	slip, err := a.Store.AuthorizeSlip(c.Request.Context(), id, actorID, middleware.ActorName(c), a.Cfg.SlipRequireSeparateAuthorizer)
	if err != nil {
		storeErr(c, err)
		return
	}
	a.notifySlipDecision(c, slip, "authorised", "")
	ok(c, slip)
}

// notifySlipDecision reports a slip sign-off to the ops desk.
//
// Unlike an asset disposal, a slip records the person it was issued to by
// name only (issued_to_name) — there is no address on the record and warehouse
// holds no user directory to resolve one. The desk address is therefore the
// only honest recipient; the issued-to name goes in the body so whoever reads
// the desk mailbox knows whose slip it is.
func (a *API) notifySlipDecision(c *gin.Context, slip models.Slip, outcome, reason string) {
	if a.Bus == nil || !a.Bus.Enabled() {
		return
	}
	// The audience carries the destination; the env desk is only the fallback
	// used until an administrator routes it, so an empty one is not a reason
	// to drop the notification any more.
	desk := events.DefaultNotifyRecipient()
	ref := slip.ID.String()
	if slip.SlipNo != nil && strings.TrimSpace(*slip.SlipNo) != "" {
		ref = strings.TrimSpace(*slip.SlipNo)
	}
	body := "Slip " + ref + " issued to " + slip.IssuedToName + " was " + outcome + "."
	if strings.TrimSpace(reason) != "" {
		body += " Reason: " + reason
	}
	a.Bus.PublishAlertTo(c.Request.Context(), "", "approvals.warehouse", desk, "approval.decision", map[string]string{
		"Title": "Slip " + outcome + ": " + ref,
		"Body":  body,
	}, slip.ID.String())
}

func (a *API) RejectSlip(c *gin.Context) {
	id, okID := parsePathID(c)
	if !okID {
		return
	}
	var body struct {
		Reason string `json:"reason"`
	}
	if err := bindJSONCoerced(c, &body); err != nil {
		badRequest(c, "invalid JSON")
		return
	}
	var actorID *uuid.UUID
	if uid, okUID := middleware.UserID(c); okUID {
		actorID = &uid
	}
	slip, err := a.Store.RejectSlip(c.Request.Context(), id, body.Reason, actorID, middleware.ActorName(c))
	if err != nil {
		storeErr(c, err)
		return
	}
	a.notifySlipDecision(c, slip, "rejected", body.Reason)
	ok(c, slip)
}

// VerifySlip is the gate check: the guard scans the barcode and is told whether
// this slip is real and whether it is still good to release.
//
// The response leads with a plain valid/not-valid verdict rather than making the
// terminal work it out from the status, because the person reading it is
// standing in front of an idling lorry.
func (a *API) VerifySlip(c *gin.Context) {
	token := c.Param("token")
	if token == "" {
		token = c.Query("token")
	}
	slip, err := a.Store.VerifySlipToken(c.Request.Context(), token)
	if err != nil {
		storeErr(c, err)
		return
	}
	valid := slip.Status == "issued"
	reason := ""
	switch slip.Status {
	case "issued":
		reason = "authorised — may be released"
	case "released":
		reason = "already released at the gate — do not release again"
	case "returned", "closed":
		reason = "this slip is closed"
	case "draft":
		reason = "not authorised"
	case "rejected", "cancelled":
		reason = "this slip was " + slip.Status
	}
	ok(c, gin.H{"valid": valid, "verdict": reason, "slip": slip})
}

// ReleaseSlip records that the goods actually left, and who let them.
func (a *API) ReleaseSlip(c *gin.Context) {
	id, okID := parsePathID(c)
	if !okID {
		return
	}
	var body struct {
		GateName string `json:"gate_name"`
		Notes    string `json:"notes"`
	}
	_ = bindJSONCoerced(c, &body)

	var actorID *uuid.UUID
	if uid, okUID := middleware.UserID(c); okUID {
		actorID = &uid
	}
	slip, err := a.Store.ReleaseSlip(c.Request.Context(), id, body.GateName, body.Notes, actorID, middleware.ActorName(c))
	if err != nil {
		storeErr(c, err)
		return
	}
	ok(c, slip)
}

// ReturnSlip books equipment back in. Sending no lines means everything came
// back, which is the common case and should not require enumerating it.
func (a *API) ReturnSlip(c *gin.Context) {
	id, okID := parsePathID(c)
	if !okID {
		return
	}
	var body struct {
		Condition string `json:"condition"`
		Lines     []struct {
			LineID      string  `json:"line_id"`
			ReturnedQty float64 `json:"returned_qty"`
			ConditionIn string  `json:"condition_in"`
		} `json:"lines"`
	}
	if err := bindJSONCoerced(c, &body); err != nil {
		badRequest(c, "invalid JSON")
		return
	}
	ctx := c.Request.Context()

	var lines []store.SlipReturnLine
	if len(body.Lines) == 0 {
		slip, err := a.Store.GetSlip(ctx, id)
		if err != nil {
			storeErr(c, err)
			return
		}
		for _, l := range slip.Lines {
			lines = append(lines, store.SlipReturnLine{LineID: l.ID, ReturnedQty: l.Qty, ConditionIn: body.Condition})
		}
	} else {
		for _, l := range body.Lines {
			lineID, err := uuid.Parse(l.LineID)
			if err != nil {
				badRequest(c, "invalid line_id")
				return
			}
			lines = append(lines, store.SlipReturnLine{LineID: lineID, ReturnedQty: l.ReturnedQty, ConditionIn: l.ConditionIn})
		}
	}

	var actorID *uuid.UUID
	if uid, okUID := middleware.UserID(c); okUID {
		actorID = &uid
	}
	slip, err := a.Store.ReturnSlip(ctx, id, lines, body.Condition, actorID, middleware.ActorName(c))
	if err != nil {
		storeErr(c, err)
		return
	}
	ok(c, slip)
}

func (a *API) CloseSlip(c *gin.Context) {
	id, okID := parsePathID(c)
	if !okID {
		return
	}
	var body struct {
		Notes string `json:"notes"`
	}
	_ = bindJSONCoerced(c, &body)

	var actorID *uuid.UUID
	if uid, okUID := middleware.UserID(c); okUID {
		actorID = &uid
	}
	slip, err := a.Store.CloseSlip(c.Request.Context(), id, body.Notes, actorID, middleware.ActorName(c))
	if err != nil {
		storeErr(c, err)
		return
	}
	ok(c, slip)
}

func (a *API) CancelSlip(c *gin.Context) {
	id, okID := parsePathID(c)
	if !okID {
		return
	}
	var body struct {
		Reason string `json:"reason"`
	}
	_ = bindJSONCoerced(c, &body)

	var actorID *uuid.UUID
	if uid, okUID := middleware.UserID(c); okUID {
		actorID = &uid
	}
	slip, err := a.Store.CancelSlip(c.Request.Context(), id, body.Reason, actorID, middleware.ActorName(c))
	if err != nil {
		storeErr(c, err)
		return
	}
	ok(c, slip)
}

// PrintSlip renders the sheet the driver carries. HTML by default because that
// is what prints from any depot machine without a toolchain; ?format=json
// returns the same data for a client that wants to lay it out itself.
func (a *API) PrintSlip(c *gin.Context) {
	id, okID := parsePathID(c)
	if !okID {
		return
	}
	slip, err := a.Store.GetSlip(c.Request.Context(), id)
	if err != nil {
		storeErr(c, err)
		return
	}
	if c.Query("format") == "json" {
		ok(c, slip)
		return
	}
	html, err := slips.Render(slip, slips.PrintOptions{
		OrgName:   a.Cfg.OrgName,
		VerifyURL: strings.TrimRight(a.Cfg.PublicAPIURL, "/") + a.Cfg.GatewayAPIPrefix + "/slips/verify",
	})
	if err != nil {
		storeErr(c, err)
		return
	}
	c.Data(http.StatusOK, "text/html; charset=utf-8", []byte(html))
}
