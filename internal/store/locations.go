package store

import (
	"context"
	"errors"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"iag-warehouse/backend/internal/models"
)

func (s *Store) ListFacilities(ctx context.Context) ([]models.Facility, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT id, code, name, site_type, attrs, created_at, updated_at
		FROM wh_facilities ORDER BY code`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanFacilities(rows)
}

func (s *Store) CreateFacility(ctx context.Context, code, name, siteType string, attrs map[string]any) (models.Facility, error) {
	var f models.Facility
	err := s.pool.QueryRow(ctx, `
		INSERT INTO wh_facilities (code, name, site_type, attrs)
		VALUES ($1, $2, $3, $4)
		RETURNING id, code, name, site_type, attrs, created_at, updated_at`,
		code, name, siteType, attrsOrEmpty(attrs),
	).Scan(&f.ID, &f.Code, &f.Name, &f.SiteType, &f.Attrs, &f.CreatedAt, &f.UpdatedAt)
	return f, err
}

func (s *Store) GetFacilityByCode(ctx context.Context, code string) (models.Facility, error) {
	var f models.Facility
	err := s.pool.QueryRow(ctx, `
		SELECT id, code, name, site_type, attrs, created_at, updated_at
		FROM wh_facilities WHERE code = $1`, code,
	).Scan(&f.ID, &f.Code, &f.Name, &f.SiteType, &f.Attrs, &f.CreatedAt, &f.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return f, ErrNotFound
	}
	return f, err
}

func (s *Store) UpdateFacility(ctx context.Context, code string, name, siteType *string, attrs map[string]any) (models.Facility, error) {
	f, err := s.GetFacilityByCode(ctx, code)
	if err != nil {
		return f, err
	}
	if name != nil {
		f.Name = *name
	}
	if siteType != nil {
		f.SiteType = *siteType
	}
	if attrs != nil {
		f.Attrs = attrs
	}
	err = s.pool.QueryRow(ctx, `
		UPDATE wh_facilities SET name = $2, site_type = $3, attrs = $4, updated_at = NOW()
		WHERE code = $1
		RETURNING id, code, name, site_type, attrs, created_at, updated_at`,
		code, f.Name, f.SiteType, f.Attrs,
	).Scan(&f.ID, &f.Code, &f.Name, &f.SiteType, &f.Attrs, &f.CreatedAt, &f.UpdatedAt)
	return f, err
}

func (s *Store) ListZones(ctx context.Context, facilityCode string) ([]models.Zone, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT z.id, z.facility_id, z.code, z.name, z.zone_type, z.attrs, z.created_at, z.updated_at
		FROM wh_zones z
		JOIN wh_facilities f ON f.id = z.facility_id
		WHERE f.code = $1 ORDER BY z.code`, facilityCode)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanZones(rows)
}

func (s *Store) GetZoneByCode(ctx context.Context, code string) (models.Zone, error) {
	var z models.Zone
	err := s.pool.QueryRow(ctx, `
		SELECT id, facility_id, code, name, zone_type, attrs, created_at, updated_at
		FROM wh_zones WHERE code = $1`, code,
	).Scan(&z.ID, &z.FacilityID, &z.Code, &z.Name, &z.ZoneType, &z.Attrs, &z.CreatedAt, &z.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return z, ErrNotFound
	}
	return z, err
}

func (s *Store) UpdateZone(ctx context.Context, code string, name, zoneType *string, attrs map[string]any) (models.Zone, error) {
	z, err := s.GetZoneByCode(ctx, code)
	if err != nil {
		return z, err
	}
	if name != nil {
		z.Name = *name
	}
	if zoneType != nil {
		z.ZoneType = *zoneType
	}
	if attrs != nil {
		z.Attrs = attrs
	}
	err = s.pool.QueryRow(ctx, `
		UPDATE wh_zones SET name = $2, zone_type = $3, attrs = $4, updated_at = NOW()
		WHERE code = $1
		RETURNING id, facility_id, code, name, zone_type, attrs, created_at, updated_at`,
		code, z.Name, z.ZoneType, z.Attrs,
	).Scan(&z.ID, &z.FacilityID, &z.Code, &z.Name, &z.ZoneType, &z.Attrs, &z.CreatedAt, &z.UpdatedAt)
	return z, err
}

func (s *Store) CreateZone(ctx context.Context, facilityCode, code, name, zoneType string, attrs map[string]any) (models.Zone, error) {
	f, err := s.GetFacilityByCode(ctx, facilityCode)
	if err != nil {
		return models.Zone{}, err
	}
	var z models.Zone
	err = s.pool.QueryRow(ctx, `
		INSERT INTO wh_zones (facility_id, code, name, zone_type, attrs)
		VALUES ($1, $2, $3, $4, $5)
		RETURNING id, facility_id, code, name, zone_type, attrs, created_at, updated_at`,
		f.ID, code, name, zoneType, attrsOrEmpty(attrs),
	).Scan(&z.ID, &z.FacilityID, &z.Code, &z.Name, &z.ZoneType, &z.Attrs, &z.CreatedAt, &z.UpdatedAt)
	return z, err
}

func (s *Store) ListBinsByZone(ctx context.Context, zoneCode string) ([]models.Bin, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT b.id, b.zone_id, b.code, b.capacity_kg, b.temperature_band, b.status, b.attrs, b.created_at, b.updated_at
		FROM wh_bins b
		JOIN wh_zones z ON z.id = b.zone_id
		WHERE z.code = $1 ORDER BY b.code`, zoneCode)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanBins(rows)
}

func (s *Store) CreateBin(ctx context.Context, zoneCode, code string, capacityKg *float64, tempBand *string, attrs map[string]any) (models.Bin, error) {
	var zoneID uuid.UUID
	err := s.pool.QueryRow(ctx, `SELECT id FROM wh_zones WHERE code = $1`, zoneCode).Scan(&zoneID)
	if errors.Is(err, pgx.ErrNoRows) {
		return models.Bin{}, ErrNotFound
	}
	if err != nil {
		return models.Bin{}, err
	}
	var b models.Bin
	err = s.pool.QueryRow(ctx, `
		INSERT INTO wh_bins (zone_id, code, capacity_kg, temperature_band, attrs)
		VALUES ($1, $2, $3, $4, $5)
		RETURNING id, zone_id, code, capacity_kg, temperature_band, status, attrs, created_at, updated_at`,
		zoneID, code, capacityKg, tempBand, attrsOrEmpty(attrs),
	).Scan(&b.ID, &b.ZoneID, &b.Code, &b.CapacityKg, &b.TemperatureBand, &b.Status, &b.Attrs, &b.CreatedAt, &b.UpdatedAt)
	return b, err
}

func (s *Store) UpdateBin(ctx context.Context, code string, capacityKg *float64, tempBand *string, status *string, attrs map[string]any) (models.Bin, error) {
	var b models.Bin
	err := s.pool.QueryRow(ctx, `
		SELECT id, zone_id, code, capacity_kg, temperature_band, status, attrs, created_at, updated_at
		FROM wh_bins WHERE code = $1`, code,
	).Scan(&b.ID, &b.ZoneID, &b.Code, &b.CapacityKg, &b.TemperatureBand, &b.Status, &b.Attrs, &b.CreatedAt, &b.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return b, ErrNotFound
	}
	if err != nil {
		return b, err
	}
	if capacityKg != nil {
		b.CapacityKg = capacityKg
	}
	if tempBand != nil {
		b.TemperatureBand = tempBand
	}
	if status != nil {
		b.Status = *status
	}
	if attrs != nil {
		b.Attrs = attrs
	}
	err = s.pool.QueryRow(ctx, `
		UPDATE wh_bins SET capacity_kg = $2, temperature_band = $3, status = $4, attrs = $5, updated_at = NOW()
		WHERE code = $1
		RETURNING id, zone_id, code, capacity_kg, temperature_band, status, attrs, created_at, updated_at`,
		code, b.CapacityKg, b.TemperatureBand, b.Status, b.Attrs,
	).Scan(&b.ID, &b.ZoneID, &b.Code, &b.CapacityKg, &b.TemperatureBand, &b.Status, &b.Attrs, &b.CreatedAt, &b.UpdatedAt)
	return b, err
}

func (s *Store) GetBinByCode(ctx context.Context, binCode string) (models.Bin, uuid.UUID, error) {
	var b models.Bin
	var zoneID uuid.UUID
	err := s.pool.QueryRow(ctx, `
		SELECT b.id, b.zone_id, b.code, b.capacity_kg, b.temperature_band, b.status, b.attrs, b.created_at, b.updated_at
		FROM wh_bins b WHERE b.code = $1`, binCode,
	).Scan(&b.ID, &b.ZoneID, &b.Code, &b.CapacityKg, &b.TemperatureBand, &b.Status, &b.Attrs, &b.CreatedAt, &b.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return b, uuid.Nil, ErrNotFound
	}
	return b, zoneID, err
}

func (s *Store) ListBinStock(ctx context.Context, binCode string) ([]models.StockBalance, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT b.id, b.item_id, b.bin_id, b.lot_key, b.serial_key, b.qty, b.status, b.updated_at, i.sku, bn.code
		FROM wh_stock_balances b
		JOIN wh_bins bn ON bn.id = b.bin_id
		JOIN wh_items i ON i.id = b.item_id
		WHERE bn.code = $1 AND b.qty > 0
		ORDER BY i.sku`, binCode)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []models.StockBalance
	for rows.Next() {
		var bal models.StockBalance
		if err := rows.Scan(&bal.ID, &bal.ItemID, &bal.BinID, &bal.LotKey, &bal.SerialKey, &bal.Qty, &bal.Status, &bal.UpdatedAt, &bal.ItemSKU, &bal.BinCode); err != nil {
			return nil, err
		}
		out = append(out, bal)
	}
	return out, rows.Err()
}

func scanFacilities(rows pgx.Rows) ([]models.Facility, error) {
	var out []models.Facility
	for rows.Next() {
		var f models.Facility
		if err := rows.Scan(&f.ID, &f.Code, &f.Name, &f.SiteType, &f.Attrs, &f.CreatedAt, &f.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, f)
	}
	return out, rows.Err()
}

func scanZones(rows pgx.Rows) ([]models.Zone, error) {
	var out []models.Zone
	for rows.Next() {
		var z models.Zone
		if err := rows.Scan(&z.ID, &z.FacilityID, &z.Code, &z.Name, &z.ZoneType, &z.Attrs, &z.CreatedAt, &z.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, z)
	}
	return out, rows.Err()
}

func scanBins(rows pgx.Rows) ([]models.Bin, error) {
	var out []models.Bin
	for rows.Next() {
		var b models.Bin
		if err := rows.Scan(&b.ID, &b.ZoneID, &b.Code, &b.CapacityKg, &b.TemperatureBand, &b.Status, &b.Attrs, &b.CreatedAt, &b.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, b)
	}
	return out, rows.Err()
}
