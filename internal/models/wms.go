package models

import (
	"time"

	"github.com/google/uuid"
)

// Execution-layer models: unit-of-measure conversion, directed putaway,
// replenishment, controlled cycle counting, handling units and barcodes.

const (
	PutawayFixedBin    = "fixed_bin"
	PutawayConsolidate = "consolidate"
	PutawayEmptyBin    = "empty_bin"
	PutawayLeastUsed   = "least_used"
	PutawayCapacityFit = "capacity_fit"

	ReplenOpen      = "open"
	ReplenCompleted = "completed"
	ReplenCancelled = "cancelled"

	ReplenTriggerMinMax = "min_max"
	ReplenTriggerManual = "manual"

	CountCounting  = "counting"
	CountReview    = "review"
	CountApproved  = "approved"
	CountCancelled = "cancelled"

	CountLinePending  = "pending"
	CountLineCounted  = "counted"
	CountLineAccepted = "accepted"
	CountLineRejected = "rejected"
	CountLineRecount  = "recount"

	CountScopeZone = "zone"
	CountScopeBin  = "bin"
	CountScopeItem = "item"
	CountScopeABC  = "abc"

	HUOpen      = "open"
	HUClosed    = "closed"
	HUShipped   = "shipped"
	HUConsumed  = "consumed"
	HUCancelled = "cancelled"

	BarcodeItem         = "item"
	BarcodeBin          = "bin"
	BarcodeAsset        = "asset"
	BarcodeHandlingUnit = "handling_unit"
	BarcodeLot          = "lot"
)

// ItemUOM is an alternate unit an item can be transacted in. Factor is the
// number of the item's base units one alternate unit contains.
type ItemUOM struct {
	ID                uuid.UUID `json:"id"`
	ItemID            uuid.UUID `json:"item_id"`
	UOM               string    `json:"uom"`
	Factor            float64   `json:"factor"`
	IsPurchaseDefault bool      `json:"is_purchase_default"`
	IsSalesDefault    bool      `json:"is_sales_default"`
	CreatedAt         time.Time `json:"created_at"`
	UpdatedAt         time.Time `json:"updated_at"`
}

// PutawayRule matches inbound stock to a target bin. Every match criterion is
// optional; a nil criterion matches anything.
type PutawayRule struct {
	ID             uuid.UUID  `json:"id"`
	Name           string     `json:"name"`
	Priority       int        `json:"priority"`
	Active         bool       `json:"active"`
	ItemID         *uuid.UUID `json:"item_id,omitempty"`
	MaterialClass  *string    `json:"material_class,omitempty"`
	TrackingMode   *string    `json:"tracking_mode,omitempty"`
	FacilityID     *uuid.UUID `json:"facility_id,omitempty"`
	TargetZoneID   *uuid.UUID `json:"target_zone_id,omitempty"`
	TargetZoneType *string    `json:"target_zone_type,omitempty"`
	TargetBinID    *uuid.UUID `json:"target_bin_id,omitempty"`
	Strategy       string     `json:"strategy"`
	Notes          *string    `json:"notes,omitempty"`
	CreatedBy      *uuid.UUID `json:"created_by,omitempty"`
	CreatedAt      time.Time  `json:"created_at"`
	UpdatedAt      time.Time  `json:"updated_at"`
}

// PutawaySuggestion is the outcome of evaluating the putaway rules: which bin,
// and which rule decided it.
type PutawaySuggestion struct {
	BinID       uuid.UUID  `json:"bin_id"`
	BinCode     string     `json:"bin_code"`
	ZoneCode    string     `json:"zone_code"`
	RuleID      *uuid.UUID `json:"rule_id,omitempty"`
	RuleName    string     `json:"rule_name,omitempty"`
	Strategy    string     `json:"strategy"`
	Reason      string     `json:"reason"`
	FreeKg      *float64   `json:"free_kg,omitempty"`
	ExistingQty float64    `json:"existing_qty"`
}

// ReplenLevel is a min/max on one bin — normally a pick face — that the
// replenishment generator tops back up from bulk stock.
type ReplenLevel struct {
	ID           uuid.UUID  `json:"id"`
	ItemID       uuid.UUID  `json:"item_id"`
	BinID        uuid.UUID  `json:"bin_id"`
	MinQty       float64    `json:"min_qty"`
	MaxQty       float64    `json:"max_qty"`
	SourceZoneID *uuid.UUID `json:"source_zone_id,omitempty"`
	Active       bool       `json:"active"`
	CreatedBy    *uuid.UUID `json:"created_by,omitempty"`
	CreatedAt    time.Time  `json:"created_at"`
	UpdatedAt    time.Time  `json:"updated_at"`
	// Display joins.
	ItemSKU   string  `json:"item_sku,omitempty"`
	ItemName  string  `json:"item_name,omitempty"`
	BinCode   string  `json:"bin_code,omitempty"`
	OnHand    float64 `json:"on_hand"`
	Available float64 `json:"available"`
}

// ReplenTask is one move-this-stock-there instruction. Open tasks hold a
// reservation against their source bin.
type ReplenTask struct {
	ID          uuid.UUID  `json:"id"`
	ItemID      uuid.UUID  `json:"item_id"`
	FromBinID   uuid.UUID  `json:"from_bin_id"`
	ToBinID     uuid.UUID  `json:"to_bin_id"`
	LotKey      string     `json:"lot_key"`
	Qty         float64    `json:"qty"`
	MovedQty    float64    `json:"moved_qty"`
	Status      string     `json:"status"`
	Trigger     string     `json:"trigger"`
	LevelID     *uuid.UUID `json:"level_id,omitempty"`
	Notes       *string    `json:"notes,omitempty"`
	CreatedBy   *uuid.UUID `json:"created_by,omitempty"`
	CompletedBy *uuid.UUID `json:"completed_by,omitempty"`
	CreatedAt   time.Time  `json:"created_at"`
	CompletedAt *time.Time `json:"completed_at,omitempty"`
	// Display joins.
	ItemSKU     string `json:"item_sku,omitempty"`
	ItemName    string `json:"item_name,omitempty"`
	FromBinCode string `json:"from_bin_code,omitempty"`
	ToBinCode   string `json:"to_bin_code,omitempty"`
}

// CountTask is a scoped stock count that must be counted, submitted and
// approved before any balance changes.
type CountTask struct {
	ID             uuid.UUID   `json:"id"`
	Code           string      `json:"code"`
	Status         string      `json:"status"`
	ScopeType      string      `json:"scope_type"`
	ScopeRef       string      `json:"scope_ref"`
	Blind          bool        `json:"blind"`
	TolerancePct   float64     `json:"tolerance_pct"`
	ToleranceValue float64     `json:"tolerance_value"`
	Notes          *string     `json:"notes,omitempty"`
	CreatedBy      *uuid.UUID  `json:"created_by,omitempty"`
	SubmittedBy    *uuid.UUID  `json:"submitted_by,omitempty"`
	ApprovedBy     *uuid.UUID  `json:"approved_by,omitempty"`
	CreatedAt      time.Time   `json:"created_at"`
	SubmittedAt    *time.Time  `json:"submitted_at,omitempty"`
	ApprovedAt     *time.Time  `json:"approved_at,omitempty"`
	UpdatedAt      time.Time   `json:"updated_at"`
	Lines          []CountLine `json:"lines,omitempty"`
	// Rollups computed on read.
	LineCount     int     `json:"line_count"`
	CountedCount  int     `json:"counted_count"`
	VarianceLines int     `json:"variance_lines"`
	VarianceValue float64 `json:"variance_value"`
}

// CountLine is one (item, bin, lot, serial) position within a count task.
// SystemQty is withheld from the response while a blind count is being counted.
type CountLine struct {
	ID            uuid.UUID  `json:"id"`
	CountTaskID   uuid.UUID  `json:"count_task_id"`
	ItemID        uuid.UUID  `json:"item_id"`
	BinID         uuid.UUID  `json:"bin_id"`
	LotKey        string     `json:"lot_key"`
	SerialKey     string     `json:"serial_key"`
	SystemQty     *float64   `json:"system_qty,omitempty"`
	CountedQty    *float64   `json:"counted_qty,omitempty"`
	VarianceQty   float64    `json:"variance_qty"`
	VarianceValue float64    `json:"variance_value"`
	Status        string     `json:"status"`
	Note          *string    `json:"note,omitempty"`
	CountedBy     *uuid.UUID `json:"counted_by,omitempty"`
	CountedAt     *time.Time `json:"counted_at,omitempty"`
	AdjustmentID  *uuid.UUID `json:"adjustment_id,omitempty"`
	// Display joins.
	ItemSKU  string `json:"item_sku,omitempty"`
	ItemName string `json:"item_name,omitempty"`
	BinCode  string `json:"bin_code,omitempty"`
}

// ABCResult is one item's reclassification from an ABC run.
type ABCResult struct {
	ItemID        uuid.UUID `json:"item_id"`
	SKU           string    `json:"sku"`
	Name          string    `json:"name"`
	Value         float64   `json:"value"`
	CumulativePct float64   `json:"cumulative_pct"`
	Class         string    `json:"abc_class"`
	Previous      *string   `json:"previous_class,omitempty"`
}

// HandlingUnit is a licence-plated container. Its contents mirror stock that
// wh_stock_balances still owns.
type HandlingUnit struct {
	ID         uuid.UUID      `json:"id"`
	LPN        string         `json:"lpn"`
	HUType     string         `json:"hu_type"`
	Status     string         `json:"status"`
	BinID      *uuid.UUID     `json:"bin_id,omitempty"`
	ParentHUID *uuid.UUID     `json:"parent_hu_id,omitempty"`
	Attrs      map[string]any `json:"attrs,omitempty"`
	CreatedBy  *uuid.UUID     `json:"created_by,omitempty"`
	CreatedAt  time.Time      `json:"created_at"`
	UpdatedAt  time.Time      `json:"updated_at"`
	ClosedAt   *time.Time     `json:"closed_at,omitempty"`
	Contents   []HUContent    `json:"contents,omitempty"`
	// Display joins.
	BinCode    string `json:"bin_code,omitempty"`
	ParentLPN  string `json:"parent_lpn,omitempty"`
	ChildCount int    `json:"child_count"`
}

type HUContent struct {
	ID        uuid.UUID `json:"id"`
	HUID      uuid.UUID `json:"hu_id"`
	ItemID    uuid.UUID `json:"item_id"`
	LotKey    string    `json:"lot_key"`
	SerialKey string    `json:"serial_key"`
	Qty       float64   `json:"qty"`
	UpdatedAt time.Time `json:"updated_at"`
	// Display joins.
	ItemSKU  string `json:"item_sku,omitempty"`
	ItemName string `json:"item_name,omitempty"`
}

// Barcode resolves a scanned string to an entity. For item barcodes, UOM and
// QtyPerScan describe how much one scan represents in base units.
type Barcode struct {
	ID         uuid.UUID  `json:"id"`
	Barcode    string     `json:"barcode"`
	EntityType string     `json:"entity_type"`
	EntityID   *uuid.UUID `json:"entity_id,omitempty"`
	LotKey     string     `json:"lot_key"`
	UOM        string     `json:"uom"`
	QtyPerScan float64    `json:"qty_per_scan"`
	Active     bool       `json:"active"`
	CreatedBy  *uuid.UUID `json:"created_by,omitempty"`
	CreatedAt  time.Time  `json:"created_at"`
	UpdatedAt  time.Time  `json:"updated_at"`
}

// ScanResult is what a terminal gets back for a scanned string: what it is, plus
// enough of the entity to display without a second round trip.
type ScanResult struct {
	Barcode      string        `json:"barcode"`
	EntityType   string        `json:"entity_type"`
	Item         *Item         `json:"item,omitempty"`
	Bin          *Bin          `json:"bin,omitempty"`
	Asset        *Asset        `json:"asset,omitempty"`
	HandlingUnit *HandlingUnit `json:"handling_unit,omitempty"`
	LotKey       string        `json:"lot_key,omitempty"`
	UOM          string        `json:"uom,omitempty"`
	QtyPerScan   float64       `json:"qty_per_scan,omitempty"`
	// Balances at the scanned location or for the scanned item, so a terminal can
	// show what is there straight away.
	Balances []StockBalance `json:"balances,omitempty"`
}
