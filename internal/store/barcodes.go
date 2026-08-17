package store

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"iag-warehouse/backend/internal/models"
)

// Barcodes and scan resolution.
//
// A terminal sends a string it knows nothing about and is told what it is. The
// registry is checked first; failing that, the string is tried against the
// natural keys people already print on things — bin codes, SKUs, asset tags,
// licence plates — so scanning works on day one, before anyone has registered a
// single label, and the registry becomes the way to add the labels that do not
// match a natural key (a supplier's GTIN, a case barcode, a relabelled bin).

const barcodeCols = `id, barcode, entity_type, entity_id, lot_key, uom, qty_per_scan, active,
	created_by, created_at, updated_at`

func scanBarcode(row pgx.Row) (models.Barcode, error) {
	var b models.Barcode
	err := row.Scan(&b.ID, &b.Barcode, &b.EntityType, &b.EntityID, &b.LotKey, &b.UOM, &b.QtyPerScan,
		&b.Active, &b.CreatedBy, &b.CreatedAt, &b.UpdatedAt)
	return b, err
}

type BarcodeInput struct {
	Barcode    string
	EntityType string
	EntityID   *uuid.UUID
	LotKey     string
	UOM        string
	CreatedBy  *uuid.UUID
}

func (s *Store) ListBarcodes(ctx context.Context, entityType string, limit int) ([]models.Barcode, error) {
	if limit <= 0 || limit > 500 {
		limit = 200
	}
	query := `SELECT ` + barcodeCols + ` FROM wh_barcodes`
	args := []any{}
	if entityType != "" {
		args = append(args, entityType)
		query += ` WHERE entity_type = $1`
	}
	args = append(args, limit)
	query += fmt.Sprintf(` ORDER BY created_at DESC LIMIT $%d`, len(args))

	rows, err := s.pool.Query(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []models.Barcode{}
	for rows.Next() {
		b, err := scanBarcode(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, b)
	}
	return out, rows.Err()
}

// CreateBarcode registers a label. For an item label the unit decides how much
// one scan is worth: the conversion factor is resolved now and stored, so
// re-defining a case size later cannot retroactively change what yesterday's
// scans meant.
func (s *Store) CreateBarcode(ctx context.Context, in BarcodeInput) (models.Barcode, error) {
	code := strings.TrimSpace(in.Barcode)
	if code == "" {
		return models.Barcode{}, fmt.Errorf("%w: barcode is required", ErrInvalidArgument)
	}
	switch in.EntityType {
	case models.BarcodeItem, models.BarcodeBin, models.BarcodeAsset, models.BarcodeHandlingUnit:
		if in.EntityID == nil {
			return models.Barcode{}, fmt.Errorf("%w: entity_id is required for a %s barcode", ErrInvalidArgument, in.EntityType)
		}
	case models.BarcodeLot:
		if strings.TrimSpace(in.LotKey) == "" {
			return models.Barcode{}, fmt.Errorf("%w: lot_key is required for a lot barcode", ErrInvalidArgument)
		}
	default:
		return models.Barcode{}, fmt.Errorf("%w: unknown entity_type %q", ErrInvalidArgument, in.EntityType)
	}

	qtyPerScan := 1.0
	uom := strings.TrimSpace(in.UOM)
	if in.EntityType == models.BarcodeItem && uom != "" {
		conv, err := s.ConvertToBase(ctx, *in.EntityID, 1, uom)
		if err != nil {
			return models.Barcode{}, err
		}
		qtyPerScan = conv.BaseQty
	}

	b, err := scanBarcode(s.pool.QueryRow(ctx, `
		INSERT INTO wh_barcodes (barcode, entity_type, entity_id, lot_key, uom, qty_per_scan, created_by)
		VALUES ($1,$2,$3,$4,$5,$6,$7) RETURNING `+barcodeCols,
		code, in.EntityType, in.EntityID, in.LotKey, uom, qtyPerScan, in.CreatedBy))
	if err != nil && isUniqueViolation(err) {
		return models.Barcode{}, ErrConflict
	}
	return b, err
}

func (s *Store) DeleteBarcode(ctx context.Context, id uuid.UUID) error {
	tag, err := s.pool.Exec(ctx, `DELETE FROM wh_barcodes WHERE id = $1`, id)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// ResolveBarcode turns a scanned string into whatever it identifies, together
// with the stock standing at (or of) it, so a terminal can show something useful
// without a second round trip.
func (s *Store) ResolveBarcode(ctx context.Context, code string) (models.ScanResult, error) {
	code = strings.TrimSpace(code)
	if code == "" {
		return models.ScanResult{}, fmt.Errorf("%w: nothing was scanned", ErrInvalidArgument)
	}
	res := models.ScanResult{Barcode: code, QtyPerScan: 1}

	reg, err := scanBarcode(s.pool.QueryRow(ctx,
		`SELECT `+barcodeCols+` FROM wh_barcodes WHERE barcode = $1 AND active`, code))
	if err == nil {
		res.EntityType = reg.EntityType
		res.LotKey = reg.LotKey
		res.UOM = reg.UOM
		res.QtyPerScan = reg.QtyPerScan
		return s.hydrateScan(ctx, res, reg.EntityID)
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return res, err
	}

	// Not registered — try the natural keys, most specific first. Bin before SKU
	// because a bin code is the thing most often stencilled on a rack, and the two
	// namespaces are separate in practice.
	if bin, _, err := s.GetBinByCode(ctx, code); err == nil {
		res.EntityType = models.BarcodeBin
		return s.hydrateScan(ctx, res, &bin.ID)
	} else if !errors.Is(err, ErrNotFound) {
		return res, err
	}
	if item, err := s.GetItemBySKU(ctx, code); err == nil {
		res.EntityType = models.BarcodeItem
		return s.hydrateScan(ctx, res, &item.ID)
	} else if !errors.Is(err, ErrNotFound) {
		return res, err
	}
	if hu, err := s.GetHandlingUnitByLPN(ctx, code); err == nil {
		res.EntityType = models.BarcodeHandlingUnit
		res.HandlingUnit = &hu
		return res, nil
	} else if !errors.Is(err, ErrNotFound) {
		return res, err
	}
	if asset, err := s.GetAssetByTag(ctx, code); err == nil {
		res.EntityType = models.BarcodeAsset
		res.Asset = &asset
		return res, nil
	} else if !errors.Is(err, ErrNotFound) {
		return res, err
	}
	return res, ErrNotFound
}

func (s *Store) hydrateScan(ctx context.Context, res models.ScanResult, entityID *uuid.UUID) (models.ScanResult, error) {
	if entityID == nil {
		return res, nil
	}
	switch res.EntityType {
	case models.BarcodeItem:
		item, err := s.GetItem(ctx, *entityID)
		if err != nil {
			return res, err
		}
		res.Item = &item
		balances, err := s.ListItemBalances(ctx, item.ID)
		if err != nil {
			return res, err
		}
		res.Balances = balances
	case models.BarcodeBin:
		bin, err := s.getBinByID(ctx, *entityID)
		if err != nil {
			return res, err
		}
		res.Bin = &bin
		balances, err := s.ListBinStock(ctx, bin.Code)
		if err != nil {
			return res, err
		}
		res.Balances = balances
	case models.BarcodeAsset:
		asset, err := s.getAssetByID(ctx, *entityID)
		if err != nil {
			return res, err
		}
		res.Asset = &asset
	case models.BarcodeHandlingUnit:
		hu, err := s.GetHandlingUnit(ctx, *entityID)
		if err != nil {
			return res, err
		}
		res.HandlingUnit = &hu
	}
	return res, nil
}

func (s *Store) getBinByID(ctx context.Context, id uuid.UUID) (models.Bin, error) {
	var b models.Bin
	err := s.pool.QueryRow(ctx, `
		SELECT id, zone_id, code, capacity_kg, temperature_band, status, attrs, created_at, updated_at
		FROM wh_bins WHERE id = $1`, id,
	).Scan(&b.ID, &b.ZoneID, &b.Code, &b.CapacityKg, &b.TemperatureBand, &b.Status, &b.Attrs, &b.CreatedAt, &b.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return b, ErrNotFound
	}
	return b, err
}
