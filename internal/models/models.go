package models

import (
	"encoding/json"
	"time"

	"github.com/google/uuid"
)

const (
	MaterialRawMaterial  = "raw_material"
	MaterialFinishedGood = "finished_good"
	MaterialConsumable   = "consumable"
	MaterialSparePart    = "spare_part"
	MaterialEquipment    = "equipment"

	TrackingBulk   = "bulk"
	TrackingLot    = "lot"
	TrackingSKU    = "sku"
	TrackingSerial = "serial"

	StatusAvailable = "available"
	StatusHold      = "hold"
	StatusDamaged   = "damaged"

	ReceiptDraft  = "draft"
	ReceiptPosted = "posted"

	IssueDraft  = "draft"
	IssuePosted = "posted"

	MovementReceipt           = "receipt"
	MovementIssue             = "issue"
	MovementTransfer          = "transfer"
	MovementProductionConsume = "production_consume"
	MovementProductionOutput  = "production_output"
	MovementReturn            = "return"
	MovementAdjustment        = "adjustment"
	MovementAssetCheckin      = "asset_checkin"
	MovementAssetCheckout     = "asset_checkout"
	MovementAssetDispose      = "asset_dispose"
	MovementPick              = "pick"
)

type Facility struct {
	ID        uuid.UUID      `json:"id"`
	Code      string         `json:"code"`
	Name      string         `json:"name"`
	SiteType  string         `json:"site_type"`
	Attrs     map[string]any `json:"attrs,omitempty"`
	CreatedAt time.Time      `json:"created_at"`
	UpdatedAt time.Time      `json:"updated_at"`
}

type Zone struct {
	ID         uuid.UUID      `json:"id"`
	FacilityID uuid.UUID      `json:"facility_id"`
	Code       string         `json:"code"`
	Name       string         `json:"name"`
	ZoneType   string         `json:"zone_type"`
	Attrs      map[string]any `json:"attrs,omitempty"`
	CreatedAt  time.Time      `json:"created_at"`
	UpdatedAt  time.Time      `json:"updated_at"`
}

type Bin struct {
	ID              uuid.UUID      `json:"id"`
	ZoneID          uuid.UUID      `json:"zone_id"`
	Code            string         `json:"code"`
	CapacityKg      *float64       `json:"capacity_kg,omitempty"`
	TemperatureBand *string        `json:"temperature_band,omitempty"`
	Status          string         `json:"status"`
	Attrs           map[string]any `json:"attrs,omitempty"`
	CreatedAt       time.Time      `json:"created_at"`
	UpdatedAt       time.Time      `json:"updated_at"`
}

type Item struct {
	ID            uuid.UUID      `json:"id"`
	SKU           string         `json:"sku"`
	Name          string         `json:"name"`
	MaterialClass string         `json:"material_class"`
	TrackingMode  string         `json:"tracking_mode"`
	UOM           string         `json:"uom"`
	MinQty        float64        `json:"min_qty"`
	MaxQty        *float64       `json:"max_qty,omitempty"`
	Attrs         map[string]any `json:"attrs,omitempty"`
	CreatedAt     time.Time      `json:"created_at"`
	UpdatedAt     time.Time      `json:"updated_at"`
}

type StockBalance struct {
	ID        uuid.UUID `json:"id"`
	ItemID    uuid.UUID `json:"item_id"`
	BinID     uuid.UUID `json:"bin_id"`
	LotKey    string    `json:"lot_key"`
	SerialKey string    `json:"serial_key"`
	Qty       float64   `json:"qty"`
	Reserved  float64   `json:"reserved"`
	Available float64   `json:"available"`
	Status    string    `json:"status"`
	UpdatedAt time.Time `json:"updated_at"`
	ItemSKU   string    `json:"item_sku,omitempty"`
	BinCode   string    `json:"bin_code,omitempty"`
}

type Receipt struct {
	ID          uuid.UUID     `json:"id"`
	ReceiptType string        `json:"receipt_type"`
	Status      string        `json:"status"`
	SourceRef   *string       `json:"source_ref,omitempty"`
	GRNID       *string       `json:"grn_id,omitempty"`
	POID        *string       `json:"po_id,omitempty"`
	Supplier    *string       `json:"supplier,omitempty"`
	ReceivedBy  *string       `json:"received_by,omitempty"`
	Notes       *string       `json:"notes,omitempty"`
	// Value is the receipt's total cost, computed on read from the priced lines
	// (Σ qty*unit_cost) — not a stored column.
	Value     float64       `json:"value"`
	PostedAt  *time.Time    `json:"posted_at,omitempty"`
	CreatedBy *uuid.UUID    `json:"created_by,omitempty"`
	CreatedAt time.Time     `json:"created_at"`
	UpdatedAt time.Time     `json:"updated_at"`
	Lines     []ReceiptLine `json:"lines,omitempty"`
}

type ReceiptLine struct {
	ID              uuid.UUID      `json:"id"`
	ReceiptID       uuid.UUID      `json:"receipt_id"`
	ItemID          uuid.UUID      `json:"item_id"`
	Qty             float64        `json:"qty"`
	UOM             string         `json:"uom"`
	BinID           uuid.UUID      `json:"bin_id"`
	BinCode         string         `json:"bin_code,omitempty"`
	LotKey          string         `json:"lot_key"`
	BatchBusinessID *string        `json:"batch_business_id,omitempty"`
	Attrs           map[string]any `json:"attrs,omitempty"`
}

type Issue struct {
	ID                 uuid.UUID   `json:"id"`
	Status             string      `json:"status"`
	Department         *string     `json:"department,omitempty"`
	CostCenter         *string     `json:"cost_center,omitempty"`
	ProductionOrderRef *string     `json:"production_order_ref,omitempty"`
	WorkOrderRef       *string     `json:"work_order_ref,omitempty"`
	RequestedBy        *string     `json:"requested_by,omitempty"`
	Priority           *string     `json:"priority,omitempty"`
	BatchBusinessID    *string     `json:"batch_business_id,omitempty"`
	Notes              *string     `json:"notes,omitempty"`
	PostedAt           *time.Time  `json:"posted_at,omitempty"`
	CreatedBy          *uuid.UUID  `json:"created_by,omitempty"`
	CreatedAt          time.Time   `json:"created_at"`
	UpdatedAt          time.Time   `json:"updated_at"`
	Lines              []IssueLine `json:"lines,omitempty"`
}

type IssueLine struct {
	ID        uuid.UUID      `json:"id"`
	IssueID   uuid.UUID      `json:"issue_id"`
	ItemID    uuid.UUID      `json:"item_id"`
	Qty       float64        `json:"qty"`
	UOM       string         `json:"uom"`
	BinID     uuid.UUID      `json:"bin_id"`
	BinCode   string         `json:"bin_code,omitempty"`
	LotKey    string         `json:"lot_key"`
	SerialKey string         `json:"serial_key"`
	Attrs     map[string]any `json:"attrs,omitempty"`
}

type Transfer struct {
	ID             uuid.UUID      `json:"id"`
	Status         string         `json:"status"`
	FromFacilityID *uuid.UUID     `json:"from_facility_id,omitempty"`
	ToFacilityID   *uuid.UUID     `json:"to_facility_id,omitempty"`
	Notes          *string        `json:"notes,omitempty"`
	PostedAt       *time.Time     `json:"posted_at,omitempty"`
	CreatedBy      *uuid.UUID     `json:"created_by,omitempty"`
	CreatedAt      time.Time      `json:"created_at"`
	UpdatedAt      time.Time      `json:"updated_at"`
	Lines          []TransferLine `json:"lines,omitempty"`
}

type TransferLine struct {
	ID         uuid.UUID      `json:"id"`
	TransferID uuid.UUID      `json:"transfer_id"`
	ItemID     uuid.UUID      `json:"item_id"`
	Qty        float64        `json:"qty"`
	FromBinID  uuid.UUID      `json:"from_bin_id"`
	ToBinID    uuid.UUID      `json:"to_bin_id"`
	LotKey     string         `json:"lot_key"`
	SerialKey  string         `json:"serial_key"`
	Attrs      map[string]any `json:"attrs,omitempty"`
}

type Asset struct {
	ID           uuid.UUID      `json:"id"`
	AssetTag     string         `json:"asset_tag"`
	SerialNo     *string        `json:"serial_no,omitempty"`
	ItemID       uuid.UUID      `json:"item_id"`
	CurrentBinID *uuid.UUID     `json:"current_bin_id,omitempty"`
	Condition    string         `json:"condition"`
	BookValueRef *string        `json:"book_value_ref,omitempty"`
	Attrs        map[string]any `json:"attrs,omitempty"`
	DisposedAt   *time.Time     `json:"disposed_at,omitempty"`
	CreatedAt    time.Time      `json:"created_at"`
	UpdatedAt    time.Time      `json:"updated_at"`
}

// AssetDisposal records the one-way retirement of a serialized asset out of
// stores, with the method, any sale proceeds, and the authorization/gate pass.
type AssetDisposal struct {
	ID           uuid.UUID  `json:"id"`
	AssetID      uuid.UUID  `json:"asset_id"`
	AssetTag     string     `json:"asset_tag"`
	Method       string     `json:"method"`
	Reason       string     `json:"reason"`
	Proceeds     float64    `json:"proceeds"`
	Currency     string     `json:"currency"`
	BookValue    *float64   `json:"book_value,omitempty"`
	GatePassNo    string     `json:"gate_pass_no"`
	AuthorizedBy  string     `json:"authorized_by"`
	DisposedBy    *uuid.UUID `json:"disposed_by,omitempty"`
	Status        string     `json:"status"`
	DisposalValue float64    `json:"disposal_value"`
	RequestedBy   string     `json:"requested_by"`
	CreatedAt     time.Time  `json:"created_at"`
}

type PickList struct {
	ID          uuid.UUID  `json:"id"`
	Status      string     `json:"status"`
	OrderRef    *string    `json:"order_ref,omitempty"`
	Notes       *string    `json:"notes,omitempty"`
	ConfirmedAt *time.Time `json:"confirmed_at,omitempty"`
	CreatedBy   *uuid.UUID `json:"created_by,omitempty"`
	CreatedAt   time.Time  `json:"created_at"`
	UpdatedAt   time.Time  `json:"updated_at"`
	Lines       []PickLine `json:"lines,omitempty"`
}

type PickLine struct {
	ID         uuid.UUID      `json:"id"`
	PickListID uuid.UUID      `json:"pick_list_id"`
	ItemID     uuid.UUID      `json:"item_id"`
	Qty        float64        `json:"qty"`
	BinID      uuid.UUID      `json:"bin_id"`
	LotKey     string         `json:"lot_key"`
	PickedQty  float64        `json:"picked_qty"`
	Attrs      map[string]any `json:"attrs,omitempty"`
}

type Adjustment struct {
	ID        uuid.UUID  `json:"id"`
	AdjType   string     `json:"adj_type"`
	ItemID    uuid.UUID  `json:"item_id"`
	BinID     uuid.UUID  `json:"bin_id"`
	LotKey    string     `json:"lot_key"`
	SerialKey string     `json:"serial_key"`
	QtyBefore float64    `json:"qty_before"`
	QtyAfter  float64    `json:"qty_after"`
	Reason    *string    `json:"reason,omitempty"`
	ActorID   *uuid.UUID `json:"actor_id,omitempty"`
	CreatedAt time.Time  `json:"created_at"`
	// Display joins, populated by list reads (not stored on wh_adjustments).
	ItemSKU  string `json:"item_sku,omitempty"`
	ItemName string `json:"item_name,omitempty"`
	BinCode  string `json:"bin_code,omitempty"`
}

// PackSession is a packing record created from a confirmed pick list.
type PackSession struct {
	ID         uuid.UUID      `json:"id"`
	PickListID *uuid.UUID     `json:"pick_list_id,omitempty"`
	Status     string         `json:"status"`
	Attrs      map[string]any `json:"attrs,omitempty"`
	CreatedBy  *uuid.UUID     `json:"created_by,omitempty"`
	CreatedAt  time.Time      `json:"created_at"`
}

type Movement struct {
	ID              uuid.UUID      `json:"id"`
	MovementType    string         `json:"movement_type"`
	ItemID          *uuid.UUID     `json:"item_id,omitempty"`
	FromBinID       *uuid.UUID     `json:"from_bin_id,omitempty"`
	ToBinID         *uuid.UUID     `json:"to_bin_id,omitempty"`
	Qty             float64        `json:"qty"`
	LotKey          string         `json:"lot_key"`
	SerialKey       string         `json:"serial_key"`
	RefType         *string        `json:"ref_type,omitempty"`
	RefID           *uuid.UUID     `json:"ref_id,omitempty"`
	BatchBusinessID *string        `json:"batch_business_id,omitempty"`
	ActorID         *uuid.UUID     `json:"actor_id,omitempty"`
	OccurredAt      time.Time      `json:"occurred_at"`
	Attrs           map[string]any `json:"attrs,omitempty"`
	CreatedAt       time.Time      `json:"created_at"`
}

func NormalizeLotKey(k string) string {
	if k == "" {
		return ""
	}
	return k
}

func NormalizeSerialKey(k string) string {
	if k == "" {
		return ""
	}
	return k
}

func RawJSON(v any) json.RawMessage {
	b, _ := json.Marshal(v)
	return b
}
