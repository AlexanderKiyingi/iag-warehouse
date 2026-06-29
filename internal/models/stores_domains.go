package models

import (
	"time"

	"github.com/google/uuid"
)

// Flat stores-domain records backing storesiag views that have no deeper
// warehouse model (alerts, returns, gate passes, warranties, event requests).

type StockThreshold struct {
	ID          uuid.UUID `json:"id"`
	Item        string    `json:"item"`
	Dept        string    `json:"dept"`
	CurrentQty  float64   `json:"current_qty"`
	MinQty      float64   `json:"min_qty"`
	ReorderQty  float64   `json:"reorder_qty"`
	AlertMethod string    `json:"alert_method"`
	Status      string    `json:"status"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

type StockReturn struct {
	ID         uuid.UUID `json:"id"`
	Item       string    `json:"item"`
	SKU        string    `json:"sku"`
	Qty        float64   `json:"qty"`
	ReturnedBy string    `json:"returned_by"`
	Condition  string    `json:"condition"`
	LinkedRef  string    `json:"linked_ref"`
	Action     string    `json:"action"`
	Status     string    `json:"status"`
	Notes      string    `json:"notes"`
	ReturnDate string    `json:"return_date"`
	CreatedAt  time.Time `json:"created_at"`
	UpdatedAt  time.Time `json:"updated_at"`
}

type GatePass struct {
	ID           uuid.UUID `json:"id"`
	GatePassNo   string    `json:"gate_pass_no"`
	Items        string    `json:"items"`
	IssuedTo     string    `json:"issued_to"`
	Dept         string    `json:"dept"`
	Purpose      string    `json:"purpose"`
	DateOut      string    `json:"date_out"`
	ReturnBy     string    `json:"return_by"`
	ReturnDate   string    `json:"return_date"`
	Status       string    `json:"status"`
	AuthorizedBy string    `json:"authorized_by"`
	CreatedAt    time.Time `json:"created_at"`
	UpdatedAt    time.Time `json:"updated_at"`
}

type Warranty struct {
	ID           uuid.UUID `json:"id"`
	Item         string    `json:"item"`
	Supplier     string    `json:"supplier"`
	AssetRef     string    `json:"asset_ref"`
	PurchaseDate string    `json:"purchase_date"`
	ExpiryDate   string    `json:"expiry_date"`
	Duration     string    `json:"duration"`
	Covers       string    `json:"covers"`
	Contact      string    `json:"contact"`
	Status       string    `json:"status"`
	CreatedAt    time.Time `json:"created_at"`
	UpdatedAt    time.Time `json:"updated_at"`
}

type EventRequest struct {
	ID          uuid.UUID `json:"id"`
	EventName   string    `json:"event_name"`
	Items       string    `json:"items"`
	Qty         float64   `json:"qty"`
	Dept        string    `json:"dept"`
	RequestedBy string    `json:"requested_by"`
	NeededBy    string    `json:"needed_by"`
	ReturnDate  string    `json:"return_date"`
	Status      string    `json:"status"`
	Notes       string    `json:"notes"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}
