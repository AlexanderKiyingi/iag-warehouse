package store

import (
	"context"
	"strconv"
	"strings"

	"github.com/google/uuid"

	"iag-warehouse/backend/internal/models"
)

// ListReservations projects open pick lines as stock allocations.
//
// There is no reservations table to read. `wh_stock_balances.reserved` is a
// running total per item/bin with no holder recorded, so it can answer "how
// much of this is spoken for" but not "by whom, against which order" — which
// is the only form a distribution planner can act on. The open pick line is
// the holder (see the reserve/consume/release paths in stock.go), so the join
// below reconstructs the allocation from it.
//
// Cancelled and confirmed picks are excluded by default: a cancelled pick has
// released its reservation and a confirmed one has consumed it, so neither is
// holding stock any more. `status` overrides that for history views.
func (s *Store) ListReservations(
	ctx context.Context,
	status string,
	orderRef string,
	itemID *uuid.UUID,
	facility string,
	limit int,
) ([]models.StockReservation, error) {
	if limit <= 0 || limit > 500 {
		limit = 200
	}

	query := `
		SELECT pln.id, pl.id, pl.order_ref, pl.status,
			pln.item_id, i.sku, i.name, i.uom,
			pln.qty, pln.picked_qty, pln.lot_key,
			b.code, f.code, f.name, pl.created_at
		FROM wh_pick_lines pln
		JOIN wh_pick_lists pl ON pl.id = pln.pick_list_id
		JOIN wh_items i       ON i.id = pln.item_id
		JOIN wh_bins b        ON b.id = pln.bin_id
		JOIN wh_zones z       ON z.id = b.zone_id
		JOIN wh_facilities f  ON f.id = z.facility_id`

	var args []any
	clauses := []string{}

	if status != "" {
		args = append(args, status)
		clauses = append(clauses, "pl.status = $"+strconv.Itoa(len(args)))
	} else {
		clauses = append(clauses, "pl.status = 'open'")
	}
	if orderRef != "" {
		args = append(args, orderRef)
		clauses = append(clauses, "pl.order_ref = $"+strconv.Itoa(len(args)))
	}
	if itemID != nil {
		args = append(args, *itemID)
		clauses = append(clauses, "pln.item_id = $"+strconv.Itoa(len(args)))
	}
	if facility != "" {
		args = append(args, facility)
		clauses = append(clauses, "f.code = $"+strconv.Itoa(len(args)))
	}

	query += " WHERE " + strings.Join(clauses, " AND ")
	args = append(args, limit)
	query += " ORDER BY pl.created_at DESC, pln.id LIMIT $" + strconv.Itoa(len(args))

	rows, err := s.pool.Query(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []models.StockReservation{}
	for rows.Next() {
		var r models.StockReservation
		if err := rows.Scan(
			&r.ID, &r.PickListID, &r.OrderRef, &r.Status,
			&r.ItemID, &r.SKU, &r.ItemName, &r.UOM,
			&r.Qty, &r.PickedQty, &r.LotKey,
			&r.BinCode, &r.FacilityCode, &r.FacilityName, &r.CreatedAt,
		); err != nil {
			return nil, err
		}
		// What is still held. A partially picked line has released the picked
		// portion already, so qty alone overstates the claim on free stock.
		r.ReservedQty = r.Qty - r.PickedQty
		if r.ReservedQty < 0 {
			r.ReservedQty = 0
		}
		out = append(out, r)
	}
	return out, rows.Err()
}
