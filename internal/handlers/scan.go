package handlers

import (
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"iag-warehouse/backend/internal/middleware"
	"iag-warehouse/backend/internal/models"
	"iag-warehouse/backend/internal/store"
)

// The RF surface: the barcode registry, universal scan resolution, and the
// scan-driven actions a picker performs one line at a time.

func (a *API) ListBarcodes(c *gin.Context) {
	items, err := a.Store.ListBarcodes(c.Request.Context(), c.Query("entity_type"), 200)
	if err != nil {
		storeErr(c, err)
		return
	}
	ok(c, gin.H{"items": items})
}

// CreateBarcode registers a label against something. The entity is named by its
// natural key — SKU, bin code, asset tag, LPN — since whoever is sticking labels
// on shelves is reading those off the shelf, not looking up UUIDs.
func (a *API) CreateBarcode(c *gin.Context) {
	var body struct {
		Barcode    string `json:"barcode"`
		EntityType string `json:"entity_type"`
		ItemSKU    string `json:"item_sku"`
		BinCode    string `json:"bin_code"`
		AssetTag   string `json:"asset_tag"`
		LPN        string `json:"lpn"`
		LotKey     string `json:"lot_key"`
		UOM        string `json:"uom"`
	}
	if err := bindJSONCoerced(c, &body); err != nil {
		badRequest(c, "invalid JSON")
		return
	}
	ctx := c.Request.Context()

	in := store.BarcodeInput{
		Barcode:    body.Barcode,
		EntityType: body.EntityType,
		LotKey:     body.LotKey,
		UOM:        body.UOM,
	}
	if uid, okUID := middleware.UserID(c); okUID {
		in.CreatedBy = &uid
	}

	switch body.EntityType {
	case "item":
		id, err := a.Store.GetItemIDBySKU(ctx, strings.TrimSpace(body.ItemSKU))
		if err != nil {
			badRequest(c, "unknown item_sku")
			return
		}
		in.EntityID = &id
	case "bin":
		bin, _, err := a.Store.GetBinByCode(ctx, strings.TrimSpace(body.BinCode))
		if err != nil {
			badRequest(c, "unknown bin_code")
			return
		}
		in.EntityID = &bin.ID
	case "asset":
		asset, err := a.Store.GetAssetByTag(ctx, strings.TrimSpace(body.AssetTag))
		if err != nil {
			badRequest(c, "unknown asset_tag")
			return
		}
		in.EntityID = &asset.ID
	case "handling_unit":
		hu, err := a.Store.GetHandlingUnitByLPN(ctx, strings.TrimSpace(body.LPN))
		if err != nil {
			badRequest(c, "unknown lpn")
			return
		}
		in.EntityID = &hu.ID
	case "lot":
		// A lot barcode carries only the lot key — there is no lot entity to point
		// at, and that is fine, because what a scanner needs from it is the key.
	default:
		badRequest(c, "entity_type must be item, bin, asset, handling_unit or lot")
		return
	}

	row, err := a.Store.CreateBarcode(ctx, in)
	if err != nil {
		storeErr(c, err)
		return
	}
	created(c, row)
}

func (a *API) DeleteBarcode(c *gin.Context) {
	id, okID := parsePathID(c)
	if !okID {
		return
	}
	if err := a.Store.DeleteBarcode(c.Request.Context(), id); err != nil {
		storeErr(c, err)
		return
	}
	c.Status(http.StatusNoContent)
}

// ScanResolve is the first call a terminal makes after any scan: it does not
// know what the operator pointed at, and this tells it.
func (a *API) ScanResolve(c *gin.Context) {
	code := c.Query("code")
	if code == "" {
		code = c.Param("code")
	}
	res, err := a.Store.ResolveBarcode(c.Request.Context(), code)
	if err != nil {
		storeErr(c, err)
		return
	}
	ok(c, res)
}

// ScanPick records a pick against a line by scan. It exists alongside the pick
// list's own confirm because picking and confirming happen at different moments
// and by different people — the picker walks, the supervisor closes.
func (a *API) ScanPick(c *gin.Context) {
	pickListID, lineID, okIDs := parsePickAndLineID(c)
	if !okIDs {
		return
	}
	var body struct {
		PickedQty   float64 `json:"picked_qty"`
		UOM         string  `json:"uom"`
		ShortReason string  `json:"short_reason"`
		// BinCode, when sent, is the bin the picker actually scanned. It is checked
		// against the line rather than trusted: a picker standing at the wrong bin
		// is precisely the mistake scanning is meant to catch.
		BinCode string `json:"bin_code"`
	}
	if err := bindJSONCoerced(c, &body); err != nil {
		badRequest(c, "invalid JSON")
		return
	}
	ctx := c.Request.Context()

	pl, err := a.Store.GetPickList(ctx, pickListID)
	if err != nil {
		storeErr(c, err)
		return
	}
	var line *models.PickLine
	for i := range pl.Lines {
		if pl.Lines[i].ID == lineID {
			line = &pl.Lines[i]
			break
		}
	}
	if line == nil {
		notFound(c, "pick line not found on this list")
		return
	}
	if s := strings.TrimSpace(body.BinCode); s != "" && !strings.EqualFold(s, line.BinCode) {
		c.JSON(http.StatusConflict, gin.H{
			"error":        "scanned bin does not match this pick line",
			"expected_bin": line.BinCode,
			"scanned_bin":  s,
		})
		return
	}

	pickedQty := body.PickedQty
	if body.UOM != "" {
		conv, cerr := a.Store.ConvertToBase(ctx, line.ItemID, body.PickedQty, body.UOM)
		if cerr != nil {
			storeErr(c, cerr)
			return
		}
		pickedQty = conv.BaseQty
	}

	var actorID *uuid.UUID
	if uid, okUID := middleware.UserID(c); okUID {
		actorID = &uid
	}
	updated, err := a.Store.RecordPick(ctx, pickListID, lineID, pickedQty, body.ShortReason, actorID)
	if err != nil {
		storeErr(c, err)
		return
	}
	ok(c, updated)
}

// ScanMove is a bin-to-bin move driven from a terminal: scan the item, scan
// where it came from, scan where it is going. It goes through the ordinary
// transfer path so the movement ledger records it like any other move.
func (a *API) ScanMove(c *gin.Context) {
	var body struct {
		ItemID      string  `json:"item_id"`
		ItemSKU     string  `json:"item_sku"`
		FromBinCode string  `json:"from_bin_code"`
		ToBinCode   string  `json:"to_bin_code"`
		LotKey      string  `json:"lot_key"`
		SerialKey   string  `json:"serial_key"`
		Qty         float64 `json:"qty"`
		UOM         string  `json:"uom"`
	}
	if err := bindJSONCoerced(c, &body); err != nil {
		badRequest(c, "invalid JSON")
		return
	}
	if body.FromBinCode == "" || body.ToBinCode == "" {
		badRequest(c, "from_bin_code and to_bin_code are required")
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
	var createdBy *uuid.UUID
	if uid, okUID := middleware.UserID(c); okUID {
		createdBy = &uid
	}

	a.withIdempotency(c, func() (int, any) {
		tr, terr := a.Store.CreateTransfer(ctx, store.CreateTransferInput{
			Notes:     strPtr("scan move"),
			CreatedBy: createdBy,
			Lines: []store.TransferLineInput{{
				ItemID:      itemID,
				Qty:         conv.BaseQty,
				FromBinCode: body.FromBinCode,
				ToBinCode:   body.ToBinCode,
				LotKey:      body.LotKey,
				SerialKey:   body.SerialKey,
			}},
		})
		if terr != nil {
			return statusForStoreErr(terr), gin.H{"error": messageForStoreErr(terr)}
		}
		return http.StatusCreated, tr
	})
}

func parsePickAndLineID(c *gin.Context) (uuid.UUID, uuid.UUID, bool) {
	pickListID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		badRequest(c, "invalid pick list id")
		return uuid.Nil, uuid.Nil, false
	}
	lineID, err := uuid.Parse(c.Param("lineId"))
	if err != nil {
		badRequest(c, "invalid line id")
		return uuid.Nil, uuid.Nil, false
	}
	return pickListID, lineID, true
}
