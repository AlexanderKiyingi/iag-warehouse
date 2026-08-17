package models

import (
	"time"

	"github.com/google/uuid"
)

// Equipment handover slips and cargo gate passes.
//
// Both are controlled documents: raised by one person, authorised by another,
// verified by a guard at the barrier, and — when the goods are meant to come
// back — outstanding until they do.

const (
	SlipEquipmentHandover = "equipment_handover"
	SlipCargoGatePass     = "cargo_gate_pass"

	SlipDraft     = "draft"
	SlipIssued    = "issued"
	SlipReleased  = "released"
	SlipReturned  = "returned"
	SlipClosed    = "closed"
	SlipRejected  = "rejected"
	SlipCancelled = "cancelled"
)

type Slip struct {
	ID     uuid.UUID `json:"id"`
	SlipNo *string   `json:"slip_no,omitempty"`
	// SlipType is equipment_handover or cargo_gate_pass.
	SlipType   string     `json:"slip_type"`
	Status     string     `json:"status"`
	Purpose    string     `json:"purpose"`
	Notes      string     `json:"notes"`
	FacilityID *uuid.UUID `json:"facility_id,omitempty"`

	IssuedToName  string     `json:"issued_to_name"`
	IssuedToID    *uuid.UUID `json:"issued_to_id,omitempty"`
	Dept          string     `json:"dept"`
	FromCustodian string     `json:"from_custodian"`
	ToCustodian   string     `json:"to_custodian"`

	DriverName  string `json:"driver_name"`
	DriverIDNo  string `json:"driver_id_no"`
	VehicleReg  string `json:"vehicle_reg"`
	Transporter string `json:"transporter"`
	Destination string `json:"destination"`

	Returnable        bool       `json:"returnable"`
	ReturnBy          *time.Time `json:"return_by,omitempty"`
	ReturnedAt        *time.Time `json:"returned_at,omitempty"`
	ReturnedCondition string     `json:"returned_condition"`

	RequestedBy    *uuid.UUID `json:"requested_by,omitempty"`
	RequestedName  string     `json:"requested_name"`
	AuthorizedBy   *uuid.UUID `json:"authorized_by,omitempty"`
	AuthorizedName string     `json:"authorized_name"`
	AuthorizedAt   *time.Time `json:"authorized_at,omitempty"`
	RejectReason   string     `json:"reject_reason"`

	// VerifyToken is what the printed barcode encodes. It is omitted from list
	// responses so a directory of slips is not also a directory of gate keys.
	VerifyToken      *string    `json:"verify_token,omitempty"`
	GateName         string     `json:"gate_name"`
	GateVerifiedBy   *uuid.UUID `json:"gate_verified_by,omitempty"`
	GateVerifiedName string     `json:"gate_verified_name"`
	GateVerifiedAt   *time.Time `json:"gate_verified_at,omitempty"`
	GateNotes        string     `json:"gate_notes"`

	IssueID    *uuid.UUID `json:"issue_id,omitempty"`
	PickListID *uuid.UUID `json:"pick_list_id,omitempty"`
	HUID       *uuid.UUID `json:"hu_id,omitempty"`
	RefType    string     `json:"ref_type"`
	RefID      string     `json:"ref_id"`

	Attrs     map[string]any `json:"attrs,omitempty"`
	CreatedBy *uuid.UUID     `json:"created_by,omitempty"`
	CreatedAt time.Time      `json:"created_at"`
	UpdatedAt time.Time      `json:"updated_at"`

	Lines  []SlipLine  `json:"lines,omitempty"`
	Events []SlipEvent `json:"events,omitempty"`

	// Display joins and derived flags.
	FacilityCode string `json:"facility_code,omitempty"`
	FacilityName string `json:"facility_name,omitempty"`
	HULPN        string `json:"hu_lpn,omitempty"`
	// Overdue is true for a returnable slip that is past its return date and
	// still out. It is computed on read rather than stored, because it changes
	// with the clock and not with anything anyone did.
	Overdue bool `json:"overdue"`
}

type SlipLine struct {
	ID           uuid.UUID      `json:"id"`
	SlipID       uuid.UUID      `json:"slip_id"`
	ItemID       *uuid.UUID     `json:"item_id,omitempty"`
	AssetID      *uuid.UUID     `json:"asset_id,omitempty"`
	Description  string         `json:"description"`
	Qty          float64        `json:"qty"`
	UOM          string         `json:"uom"`
	SerialNo     string         `json:"serial_no"`
	LotKey       string         `json:"lot_key"`
	ConditionOut string         `json:"condition_out"`
	ConditionIn  string         `json:"condition_in"`
	ReturnedQty  float64        `json:"returned_qty"`
	Attrs        map[string]any `json:"attrs,omitempty"`
	// Display joins.
	ItemSKU  string `json:"item_sku,omitempty"`
	AssetTag string `json:"asset_tag,omitempty"`
}

type SlipEvent struct {
	ID        uuid.UUID  `json:"id"`
	SlipID    uuid.UUID  `json:"slip_id"`
	Event     string     `json:"event"`
	ActorID   *uuid.UUID `json:"actor_id,omitempty"`
	ActorName string     `json:"actor_name"`
	Notes     string     `json:"notes"`
	CreatedAt time.Time  `json:"created_at"`
}
