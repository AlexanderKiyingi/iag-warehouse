package store

import (
	"context"
	"errors"
	"strings"

	"github.com/jackc/pgx/v5"

	"iag-warehouse/backend/internal/models"
)

// DefaultBinCodeForFacility picks the bin a stock write lands in when the
// caller named a facility but not a bin.
//
// Preference order is receiving zone first, then any active bin at the site.
// Receiving is the right default because it is where goods physically arrive
// and where directed putaway expects to find them — putting an unaddressed
// receipt straight into bulk would record stock as already put away when
// nobody has moved it. Blocked bins are never chosen: a blocked bin is blocked
// for a reason, and silently defaulting into one turns a location control into
// a surprise.
func (s *Store) DefaultBinCodeForFacility(ctx context.Context, facilityCode string) (string, error) {
	var code string
	err := s.pool.QueryRow(ctx, `
		SELECT b.code
		FROM wh_bins b
		JOIN wh_zones z ON z.id = b.zone_id
		JOIN wh_facilities f ON f.id = z.facility_id
		WHERE f.code = $1 AND b.status = 'active'
		ORDER BY (z.zone_type = 'receiving') DESC, z.code, b.code
		LIMIT 1`, strings.TrimSpace(facilityCode)).Scan(&code)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", ErrNotFound
	}
	return code, err
}

// ListStockSummary is the bulk answer to "how much of each item is on hand".
//
// The per-item endpoint (ListItemBalances) is the right shape for one item's
// bin-by-bin detail, but a list screen showing a hundred items would need a
// hundred calls to fill one column, so it doesn't — it shows nothing. One
// aggregate query serves the whole list.
//
// Items with no balance row are included at zero rather than omitted. A
// catalogued item that has never been stocked is a real answer to "how much is
// on hand", and dropping it would make the summary silently disagree with the
// item list it is joined onto in the client.
func (s *Store) ListStockSummary(ctx context.Context, facilityCode string) ([]models.ItemStockSummary, error) {
	facility := strings.TrimSpace(facilityCode)

	// The balances subquery is filtered by facility rather than the outer join,
	// so an item with no stock *at that facility* still reports zero instead of
	// dropping out of the list.
	query := `
		SELECT i.id, i.sku, i.name, i.uom,
		       COALESCE(b.qty, 0), COALESCE(b.reserved, 0), COALESCE(b.bin_count, 0),
		       b.updated_at
		FROM wh_items i
		LEFT JOIN (
			SELECT sb.item_id,
			       SUM(sb.qty) AS qty,
			       SUM(sb.reserved) AS reserved,
			       COUNT(DISTINCT sb.bin_id) AS bin_count,
			       MAX(sb.updated_at) AS updated_at
			FROM wh_stock_balances sb`
	var args []any
	if facility != "" {
		query += `
			JOIN wh_bins bn ON bn.id = sb.bin_id
			JOIN wh_zones z ON z.id = bn.zone_id
			JOIN wh_facilities f ON f.id = z.facility_id
			WHERE f.code = $1`
		args = append(args, facility)
	}
	query += `
			GROUP BY sb.item_id
		) b ON b.item_id = i.id
		ORDER BY i.sku`

	rows, err := s.pool.Query(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []models.ItemStockSummary{}
	for rows.Next() {
		var row models.ItemStockSummary
		if err := rows.Scan(&row.ItemID, &row.SKU, &row.Name, &row.UOM,
			&row.Qty, &row.Reserved, &row.BinCount, &row.UpdatedAt); err != nil {
			return nil, err
		}
		row.Available = row.Qty - row.Reserved
		out = append(out, row)
	}
	return out, rows.Err()
}
