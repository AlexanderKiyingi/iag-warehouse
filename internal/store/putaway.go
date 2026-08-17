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

// Directed putaway.
//
// Rules are evaluated in priority order and the first one that both matches the
// item and yields a legal bin wins. "Legal" always includes capacity: a rule can
// narrow where stock may go but can never direct it into a bin that cannot hold
// it, so the capacity predicate is part of the shared candidate query rather
// than something only the capacity_fit strategy applies.

// ErrNoPutawayBin means no rule produced a bin with room for the quantity. It is
// a normal outcome, not a failure — a full warehouse is a real state — so it is
// reported distinctly rather than as a missing record.
var ErrNoPutawayBin = errors.New("no putaway bin available")

type PutawayRuleInput struct {
	Name           string
	Priority       int
	Active         bool
	ItemID         *uuid.UUID
	MaterialClass  *string
	TrackingMode   *string
	FacilityID     *uuid.UUID
	TargetZoneID   *uuid.UUID
	TargetZoneType *string
	TargetBinID    *uuid.UUID
	Strategy       string
	Notes          *string
	CreatedBy      *uuid.UUID
}

const putawayRuleCols = `id, name, priority, active, item_id, material_class, tracking_mode, facility_id,
	target_zone_id, target_zone_type, target_bin_id, strategy, notes, created_by, created_at, updated_at`

func scanPutawayRule(row pgx.Row) (models.PutawayRule, error) {
	var r models.PutawayRule
	err := row.Scan(&r.ID, &r.Name, &r.Priority, &r.Active, &r.ItemID, &r.MaterialClass, &r.TrackingMode,
		&r.FacilityID, &r.TargetZoneID, &r.TargetZoneType, &r.TargetBinID, &r.Strategy, &r.Notes,
		&r.CreatedBy, &r.CreatedAt, &r.UpdatedAt)
	return r, err
}

func (s *Store) ListPutawayRules(ctx context.Context, activeOnly bool) ([]models.PutawayRule, error) {
	query := `SELECT ` + putawayRuleCols + ` FROM wh_putaway_rules`
	if activeOnly {
		query += ` WHERE active`
	}
	query += ` ORDER BY priority, created_at`
	rows, err := s.pool.Query(ctx, query)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []models.PutawayRule{}
	for rows.Next() {
		r, err := scanPutawayRule(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

func (s *Store) CreatePutawayRule(ctx context.Context, in PutawayRuleInput) (models.PutawayRule, error) {
	if strings.TrimSpace(in.Name) == "" {
		return models.PutawayRule{}, fmt.Errorf("%w: name is required", ErrInvalidArgument)
	}
	if in.Strategy == "" {
		in.Strategy = models.PutawayConsolidate
	}
	if in.Strategy == models.PutawayFixedBin && in.TargetBinID == nil {
		return models.PutawayRule{}, fmt.Errorf("%w: fixed_bin needs a target_bin_code", ErrInvalidArgument)
	}
	if in.Priority == 0 {
		in.Priority = 100
	}
	return scanPutawayRule(s.pool.QueryRow(ctx, `
		INSERT INTO wh_putaway_rules (name, priority, active, item_id, material_class, tracking_mode,
			facility_id, target_zone_id, target_zone_type, target_bin_id, strategy, notes, created_by)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13)
		RETURNING `+putawayRuleCols,
		in.Name, in.Priority, in.Active, in.ItemID, in.MaterialClass, in.TrackingMode, in.FacilityID,
		in.TargetZoneID, in.TargetZoneType, in.TargetBinID, in.Strategy, in.Notes, in.CreatedBy))
}

// UpdatePutawayRule patches a rule. Nil fields on the input are left as they
// are, which is why active is a *bool: a rule being switched off is the most
// common edit and must be distinguishable from "not mentioned".
type PutawayRulePatch struct {
	Name           *string
	Priority       *int
	Active         *bool
	Strategy       *string
	TargetZoneID   *uuid.UUID
	TargetZoneType *string
	TargetBinID    *uuid.UUID
	Notes          *string
}

func (s *Store) UpdatePutawayRule(ctx context.Context, id uuid.UUID, p PutawayRulePatch) (models.PutawayRule, error) {
	r, err := scanPutawayRule(s.pool.QueryRow(ctx, `SELECT `+putawayRuleCols+` FROM wh_putaway_rules WHERE id = $1`, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return r, ErrNotFound
	}
	if err != nil {
		return r, err
	}
	if p.Name != nil {
		r.Name = *p.Name
	}
	if p.Priority != nil {
		r.Priority = *p.Priority
	}
	if p.Active != nil {
		r.Active = *p.Active
	}
	if p.Strategy != nil {
		r.Strategy = *p.Strategy
	}
	if p.TargetZoneID != nil {
		r.TargetZoneID = p.TargetZoneID
	}
	if p.TargetZoneType != nil {
		r.TargetZoneType = p.TargetZoneType
	}
	if p.TargetBinID != nil {
		r.TargetBinID = p.TargetBinID
	}
	if p.Notes != nil {
		r.Notes = p.Notes
	}
	if r.Strategy == models.PutawayFixedBin && r.TargetBinID == nil {
		return r, fmt.Errorf("%w: fixed_bin needs a target_bin_code", ErrInvalidArgument)
	}
	return scanPutawayRule(s.pool.QueryRow(ctx, `
		UPDATE wh_putaway_rules SET name = $2, priority = $3, active = $4, strategy = $5,
			target_zone_id = $6, target_zone_type = $7, target_bin_id = $8, notes = $9, updated_at = NOW()
		WHERE id = $1 RETURNING `+putawayRuleCols,
		id, r.Name, r.Priority, r.Active, r.Strategy, r.TargetZoneID, r.TargetZoneType, r.TargetBinID, r.Notes))
}

func (s *Store) DeletePutawayRule(ctx context.Context, id uuid.UUID) error {
	tag, err := s.pool.Exec(ctx, `DELETE FROM wh_putaway_rules WHERE id = $1`, id)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// PutawayRequest asks where a quantity of an item should go. Qty is in the
// item's base unit; FacilityID narrows the search to one site when the caller
// knows it (a receipt at the mill should not be directed to the yard).
type PutawayRequest struct {
	ItemID     uuid.UUID
	Qty        float64
	LotKey     string
	FacilityID *uuid.UUID
}

// candidateQuery is the shared bin-selection query. Every strategy uses the same
// FROM and the same legality predicate; they differ only in an extra filter and
// the ordering, which is what makes "capacity is always enforced" true by
// construction rather than by remembering to repeat it.
//
// $1 item_id, $2 lot_key, $3 zone_id, $4 zone_type, $5 facility_id, $6 bin_id, $7 need_kg
const candidateQuery = `
	SELECT b.id, b.code, z.code, COALESCE(ld.load_kg, 0), b.capacity_kg, COALESCE(iq.item_qty, 0)
	FROM wh_bins b
	JOIN wh_zones z ON z.id = b.zone_id
	JOIN wh_facilities f ON f.id = z.facility_id
	LEFT JOIN LATERAL (
		SELECT COALESCE(SUM(sb.qty * i2.weight_kg), 0) AS load_kg
		FROM wh_stock_balances sb JOIN wh_items i2 ON i2.id = sb.item_id
		WHERE sb.bin_id = b.id
	) ld ON TRUE
	LEFT JOIN LATERAL (
		SELECT COALESCE(SUM(sb.qty), 0) AS item_qty
		FROM wh_stock_balances sb
		WHERE sb.bin_id = b.id AND sb.item_id = $1 AND sb.lot_key = $2
	) iq ON TRUE
	LEFT JOIN LATERAL (
		SELECT COALESCE(SUM(sb.qty), 0) AS total_qty FROM wh_stock_balances sb WHERE sb.bin_id = b.id
	) tq ON TRUE
	WHERE b.status = 'active'
	  AND ($3::uuid IS NULL OR z.id = $3)
	  AND ($4::text IS NULL OR z.zone_type = $4)
	  AND ($5::uuid IS NULL OR f.id = $5)
	  AND ($6::uuid IS NULL OR b.id = $6)
	  AND ($7::numeric <= 0 OR b.capacity_kg IS NULL OR b.capacity_kg - COALESCE(ld.load_kg, 0) >= $7)`

// ResolvePutawayBin returns where the quantity should be put and which rule said
// so. It walks the matching rules in priority order and returns the first that
// yields a bin, so a specific rule can sit in front of a catch-all default.
func (s *Store) ResolvePutawayBin(ctx context.Context, in PutawayRequest) (models.PutawaySuggestion, error) {
	var materialClass, trackingMode string
	var weightKg float64
	err := s.pool.QueryRow(ctx,
		`SELECT material_class, tracking_mode, weight_kg FROM wh_items WHERE id = $1`, in.ItemID,
	).Scan(&materialClass, &trackingMode, &weightKg)
	if errors.Is(err, pgx.ErrNoRows) {
		return models.PutawaySuggestion{}, ErrNotFound
	}
	if err != nil {
		return models.PutawaySuggestion{}, err
	}

	lotKey, _ := normalizeKeys(in.LotKey, "")
	// An unknown unit weight means the capacity constraint cannot be evaluated,
	// so it is skipped rather than assumed to be zero.
	needKg := in.Qty * weightKg

	// A facility-scoped rule only fires when the request says which site the work
	// is happening at. Letting it match a request that named no facility would
	// mean guessing, and the guess is directing goods into a bin at another site.
	rows, err := s.pool.Query(ctx, `
		SELECT `+putawayRuleCols+`
		FROM wh_putaway_rules
		WHERE active
		  AND (item_id IS NULL OR item_id = $1)
		  AND (material_class IS NULL OR material_class = $2)
		  AND (tracking_mode IS NULL OR tracking_mode = $3)
		  AND (facility_id IS NULL OR facility_id = $4)
		ORDER BY priority, created_at`,
		in.ItemID, materialClass, trackingMode, in.FacilityID)
	if err != nil {
		return models.PutawaySuggestion{}, err
	}
	defer rows.Close()
	var rules []models.PutawayRule
	for rows.Next() {
		r, err := scanPutawayRule(rows)
		if err != nil {
			return models.PutawaySuggestion{}, err
		}
		rules = append(rules, r)
	}
	if err := rows.Err(); err != nil {
		return models.PutawaySuggestion{}, err
	}

	for _, rule := range rules {
		// A rule's own facility scope wins over the request's: the rule is the
		// policy, the request is only a hint about where the work is happening.
		facilityID := rule.FacilityID
		if facilityID == nil {
			facilityID = in.FacilityID
		}
		sug, err := s.candidateForRule(ctx, rule, in.ItemID, lotKey, needKg, facilityID)
		if errors.Is(err, ErrNoPutawayBin) {
			continue
		}
		if err != nil {
			return models.PutawaySuggestion{}, err
		}
		return sug, nil
	}
	return models.PutawaySuggestion{}, ErrNoPutawayBin
}

func (s *Store) candidateForRule(ctx context.Context, rule models.PutawayRule, itemID uuid.UUID, lotKey string, needKg float64, facilityID *uuid.UUID) (models.PutawaySuggestion, error) {
	binID := rule.TargetBinID
	filter := ""
	order := " ORDER BY b.code LIMIT 1"
	reason := ""

	switch rule.Strategy {
	case models.PutawayFixedBin:
		reason = "home slot for this item"
	case models.PutawayConsolidate:
		filter = " AND COALESCE(iq.item_qty, 0) > 0"
		order = " ORDER BY iq.item_qty DESC, b.code LIMIT 1"
		reason = "consolidating with stock already held"
	case models.PutawayEmptyBin:
		filter = " AND COALESCE(tq.total_qty, 0) = 0"
		reason = "empty bin in scope"
	case models.PutawayLeastUsed:
		order = " ORDER BY COALESCE(ld.load_kg, 0), COALESCE(tq.total_qty, 0), b.code LIMIT 1"
		reason = "least loaded bin in scope"
	case models.PutawayCapacityFit:
		// Best fit is only meaningful against a declared capacity; a bin with no
		// stated capacity would always look infinitely roomy and win every time.
		filter = " AND b.capacity_kg IS NOT NULL"
		order = " ORDER BY (b.capacity_kg - COALESCE(ld.load_kg, 0)) ASC, b.code LIMIT 1"
		reason = "tightest bin that still fits"
	default:
		return models.PutawaySuggestion{}, fmt.Errorf("%w: unknown putaway strategy %q", ErrInvalidArgument, rule.Strategy)
	}

	var sug models.PutawaySuggestion
	var capacityKg *float64
	var loadKg float64
	err := s.pool.QueryRow(ctx, candidateQuery+filter+order,
		itemID, lotKey, rule.TargetZoneID, rule.TargetZoneType, facilityID, binID, needKg,
	).Scan(&sug.BinID, &sug.BinCode, &sug.ZoneCode, &loadKg, &capacityKg, &sug.ExistingQty)
	if errors.Is(err, pgx.ErrNoRows) {
		return models.PutawaySuggestion{}, ErrNoPutawayBin
	}
	if err != nil {
		return models.PutawaySuggestion{}, err
	}
	if capacityKg != nil {
		free := *capacityKg - loadKg
		sug.FreeKg = &free
	}
	ruleID := rule.ID
	sug.RuleID = &ruleID
	sug.RuleName = rule.Name
	sug.Strategy = rule.Strategy
	sug.Reason = reason
	return sug, nil
}
