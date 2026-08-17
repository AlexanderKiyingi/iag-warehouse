package handlers

import (
	"context"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"iag-warehouse/backend/internal/middleware"
	"iag-warehouse/backend/internal/store"
)

// Directed putaway rules.
//
// Rules are written in terms of codes rather than UUIDs, because the people who
// maintain them think in bin codes and facility codes and would have to look up
// an identifier for every field otherwise.

func (a *API) ListPutawayRules(c *gin.Context) {
	rules, err := a.Store.ListPutawayRules(c.Request.Context(), c.Query("active") == "true")
	if err != nil {
		storeErr(c, err)
		return
	}
	ok(c, gin.H{"items": rules})
}

type putawayRuleBody struct {
	Name           string `json:"name"`
	Priority       int    `json:"priority"`
	Active         *bool  `json:"active"`
	ItemSKU        string `json:"item_sku"`
	MaterialClass  string `json:"material_class"`
	TrackingMode   string `json:"tracking_mode"`
	FacilityCode   string `json:"facility_code"`
	TargetZoneCode string `json:"target_zone_code"`
	TargetZoneType string `json:"target_zone_type"`
	TargetBinCode  string `json:"target_bin_code"`
	Strategy       string `json:"strategy"`
	Notes          string `json:"notes"`
}

func (a *API) CreatePutawayRule(c *gin.Context) {
	var body putawayRuleBody
	if err := bindJSONCoerced(c, &body); err != nil {
		badRequest(c, "invalid JSON")
		return
	}
	ctx := c.Request.Context()

	in := store.PutawayRuleInput{
		Name:          body.Name,
		Priority:      body.Priority,
		Active:        body.Active == nil || *body.Active,
		MaterialClass: strPtr(body.MaterialClass),
		TrackingMode:  strPtr(body.TrackingMode),
		Strategy:      body.Strategy,
		Notes:         strPtr(body.Notes),
	}
	if uid, okUID := middleware.UserID(c); okUID {
		in.CreatedBy = &uid
	}
	if s := strings.TrimSpace(body.ItemSKU); s != "" {
		id, err := a.Store.GetItemIDBySKU(ctx, s)
		if err != nil {
			badRequest(c, "unknown item_sku")
			return
		}
		in.ItemID = &id
	}
	if s := strings.TrimSpace(body.FacilityCode); s != "" {
		f, err := a.Store.GetFacilityByCode(ctx, s)
		if err != nil {
			badRequest(c, "unknown facility_code")
			return
		}
		in.FacilityID = &f.ID
	}
	if s := strings.TrimSpace(body.TargetZoneCode); s != "" {
		z, err := a.Store.GetZoneByCode(ctx, s)
		if err != nil {
			badRequest(c, "unknown target_zone_code")
			return
		}
		in.TargetZoneID = &z.ID
	}
	if s := strings.TrimSpace(body.TargetZoneType); s != "" {
		in.TargetZoneType = &s
	}
	if s := strings.TrimSpace(body.TargetBinCode); s != "" {
		bin, _, err := a.Store.GetBinByCode(ctx, s)
		if err != nil {
			badRequest(c, "unknown target_bin_code")
			return
		}
		in.TargetBinID = &bin.ID
	}

	rule, err := a.Store.CreatePutawayRule(ctx, in)
	if err != nil {
		storeErr(c, err)
		return
	}
	created(c, rule)
}

func (a *API) UpdatePutawayRule(c *gin.Context) {
	id, okID := parsePathID(c)
	if !okID {
		return
	}
	var body putawayRuleBody
	if err := bindJSONCoerced(c, &body); err != nil {
		badRequest(c, "invalid JSON")
		return
	}
	patch := store.PutawayRulePatch{Active: body.Active}
	if body.Name != "" {
		patch.Name = &body.Name
	}
	if body.Priority != 0 {
		patch.Priority = &body.Priority
	}
	if body.Strategy != "" {
		patch.Strategy = &body.Strategy
	}
	if body.Notes != "" {
		patch.Notes = &body.Notes
	}
	if s := strings.TrimSpace(body.TargetZoneType); s != "" {
		patch.TargetZoneType = &s
	}
	if s := strings.TrimSpace(body.TargetZoneCode); s != "" {
		z, err := a.Store.GetZoneByCode(c.Request.Context(), s)
		if err != nil {
			badRequest(c, "unknown target_zone_code")
			return
		}
		patch.TargetZoneID = &z.ID
	}
	if s := strings.TrimSpace(body.TargetBinCode); s != "" {
		bin, _, err := a.Store.GetBinByCode(c.Request.Context(), s)
		if err != nil {
			badRequest(c, "unknown target_bin_code")
			return
		}
		patch.TargetBinID = &bin.ID
	}

	rule, err := a.Store.UpdatePutawayRule(c.Request.Context(), id, patch)
	if err != nil {
		storeErr(c, err)
		return
	}
	ok(c, rule)
}

func (a *API) DeletePutawayRule(c *gin.Context) {
	id, okID := parsePathID(c)
	if !okID {
		return
	}
	if err := a.Store.DeletePutawayRule(c.Request.Context(), id); err != nil {
		storeErr(c, err)
		return
	}
	c.Status(http.StatusNoContent)
}

// ResolvePutaway answers "where should this go" without putting anything there.
// A receiving terminal calls it to show the operator the directed bin before
// they walk, and a supervisor calls it to sanity-check a rule set.
func (a *API) ResolvePutaway(c *gin.Context) {
	var body struct {
		ItemID       string  `json:"item_id"`
		ItemSKU      string  `json:"item_sku"`
		Qty          float64 `json:"qty"`
		UOM          string  `json:"uom"`
		LotKey       string  `json:"lot_key"`
		FacilityCode string  `json:"facility_code"`
	}
	if err := bindJSONCoerced(c, &body); err != nil {
		badRequest(c, "invalid JSON")
		return
	}
	ctx := c.Request.Context()

	itemID, err := a.resolveItemID(ctx, body.ItemID, body.ItemSKU)
	if err != nil {
		badRequest(c, "item_id or item_sku is required")
		return
	}
	conv, err := a.Store.ConvertToBase(ctx, itemID, body.Qty, body.UOM)
	if err != nil {
		storeErr(c, err)
		return
	}
	req := store.PutawayRequest{ItemID: itemID, Qty: conv.BaseQty, LotKey: body.LotKey}
	if s := strings.TrimSpace(body.FacilityCode); s != "" {
		f, ferr := a.Store.GetFacilityByCode(ctx, s)
		if ferr != nil {
			badRequest(c, "unknown facility_code")
			return
		}
		req.FacilityID = &f.ID
	}

	sug, err := a.Store.ResolvePutawayBin(ctx, req)
	if err != nil {
		storeErr(c, err)
		return
	}
	ok(c, gin.H{"suggestion": sug, "base_qty": conv.BaseQty, "base_uom": conv.BaseUOM})
}

// resolveItemID accepts either an id or a SKU, since scanners produce SKUs and
// UIs produce ids and neither should have to translate for the other.
func (a *API) resolveItemID(ctx context.Context, rawID, sku string) (uuid.UUID, error) {
	if s := strings.TrimSpace(rawID); s != "" {
		return uuid.Parse(s)
	}
	return a.Store.GetItemIDBySKU(ctx, strings.TrimSpace(sku))
}
