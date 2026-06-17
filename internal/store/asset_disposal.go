package store

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"iag-warehouse/backend/internal/events"
	"iag-warehouse/backend/internal/models"
)

const disposalCols = `id, asset_id, asset_tag, method, reason, proceeds, currency, book_value, gate_pass_no, authorized_by, disposed_by, status, disposal_value, requested_by, created_at`

func scanDisposalRow(row pgx.Row) (models.AssetDisposal, error) {
	var d models.AssetDisposal
	err := row.Scan(&d.ID, &d.AssetID, &d.AssetTag, &d.Method, &d.Reason, &d.Proceeds, &d.Currency, &d.BookValue,
		&d.GatePassNo, &d.AuthorizedBy, &d.DisposedBy, &d.Status, &d.DisposalValue, &d.RequestedBy, &d.CreatedAt)
	return d, err
}

// sameActor reports whether two actor identities refer to the same person
// (segregation of duties). Blank/unknown actors never match.
func sameActor(a, b string) bool {
	a = strings.TrimSpace(a)
	b = strings.TrimSpace(b)
	if a == "" || b == "" || strings.EqualFold(a, "unknown") {
		return false
	}
	return strings.EqualFold(a, b)
}

func (s *Store) getDisposal(ctx context.Context, id uuid.UUID) (models.AssetDisposal, error) {
	d, err := scanDisposalRow(s.pool.QueryRow(ctx, `SELECT `+disposalCols+` FROM wh_asset_disposals WHERE id = $1`, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return models.AssetDisposal{}, ErrNotFound
	}
	return d, err
}

// executeDisposalTx retires the asset, posts the disposal movement, and emits
// warehouse.asset.disposed — shared by the immediate path and final approval.
func (s *Store) executeDisposalTx(ctx context.Context, tx pgx.Tx, disposalID uuid.UUID, actorID *uuid.UUID) error {
	var assetID, itemID uuid.UUID
	var currentBinID *uuid.UUID
	var assetTag, method, reason, currency, gatePass, authorizedBy string
	var proceeds float64
	var bookValue *float64
	err := tx.QueryRow(ctx, `
		SELECT d.asset_tag, d.method, d.reason, d.proceeds, d.currency, d.book_value, d.gate_pass_no, d.authorized_by,
		       a.id, a.item_id, a.current_bin_id
		FROM wh_asset_disposals d JOIN wh_assets a ON a.id = d.asset_id
		WHERE d.id = $1 FOR UPDATE`, disposalID,
	).Scan(&assetTag, &method, &reason, &proceeds, &currency, &bookValue, &gatePass, &authorizedBy, &assetID, &itemID, &currentBinID)
	if err != nil {
		return err
	}

	if _, err := tx.Exec(ctx, `
		UPDATE wh_assets SET current_bin_id = NULL, condition = 'disposed', disposed_at = NOW(), updated_at = NOW()
		WHERE id = $1`, assetID); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `UPDATE wh_asset_disposals SET status = 'executed' WHERE id = $1`, disposalID); err != nil {
		return err
	}
	if _, err := s.insertMovementTx(ctx, tx, movementInput{
		MovementType: models.MovementAssetDispose,
		ItemID:       &itemID,
		FromBinID:    currentBinID,
		Qty:          1,
		RefType:      refType("asset_disposal"),
		RefID:        &disposalID,
		ActorID:      actorID,
		Attrs:        map[string]any{"asset_tag": assetTag, "method": method},
	}); err != nil {
		return err
	}

	if s.bus != nil && s.bus.Enabled() {
		sku, err := s.getItemSKU(ctx, tx, itemID)
		if err != nil {
			return err
		}
		data := map[string]any{
			"asset_tag": assetTag, "item_id": itemID.String(), "sku": sku, "method": method,
			"reason": reason, "proceeds": proceeds, "currency": currency,
			"gate_pass_no": gatePass, "authorized_by": authorizedBy,
		}
		if bookValue != nil {
			data["book_value"] = *bookValue
		}
		if err := s.bus.PublishTx(ctx, tx, events.TypeAssetDisposed, data, assetTag); err != nil {
			return err
		}
	}
	return nil
}

// --- tiered approval -------------------------------------------------------

const disposalEpsilon = 0.005

type DisposalApprovalTier struct {
	Tier         int      `json:"tier"`
	Label        string   `json:"label"`
	MinAmount    float64  `json:"min_amount"`
	MaxAmount    *float64 `json:"max_amount,omitempty"`
	RequiredPerm string   `json:"required_perm"`
}

type DisposalApprovalProgress struct {
	RequiredTiers []int  `json:"required_tiers"`
	ApprovedTiers []int  `json:"approved_tiers"`
	NextTier      *int   `json:"next_tier,omitempty"`
	NextPerm      string `json:"next_perm,omitempty"`
	Complete      bool   `json:"complete"`
}

func (s *Store) ListDisposalApprovalTiers(ctx context.Context) ([]DisposalApprovalTier, error) {
	return scanDisposalTiers(s.pool.Query(ctx, `
		SELECT tier, label, min_amount, max_amount, required_perm FROM wh_disposal_approval_tiers ORDER BY tier`))
}

func (s *Store) listDisposalTiersTx(ctx context.Context, tx pgx.Tx) ([]DisposalApprovalTier, error) {
	return scanDisposalTiers(tx.Query(ctx, `
		SELECT tier, label, min_amount, max_amount, required_perm FROM wh_disposal_approval_tiers ORDER BY tier`))
}

func scanDisposalTiers(rows pgx.Rows, err error) ([]DisposalApprovalTier, error) {
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []DisposalApprovalTier
	for rows.Next() {
		var t DisposalApprovalTier
		if err := rows.Scan(&t.Tier, &t.Label, &t.MinAmount, &t.MaxAmount, &t.RequiredPerm); err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

// requiredDisposalTiers are the bands the disposal value reaches into — every
// one must sign off.
func requiredDisposalTiers(tiers []DisposalApprovalTier, value float64) []DisposalApprovalTier {
	var out []DisposalApprovalTier
	for _, t := range tiers {
		if value-t.MinAmount > disposalEpsilon {
			out = append(out, t)
		}
	}
	return out
}

func (s *Store) approvedDisposalTiers(ctx context.Context, tx pgx.Tx, disposalID uuid.UUID) ([]int, error) {
	rows, err := tx.Query(ctx, `
		SELECT tier FROM wh_disposal_approvals WHERE disposal_id = $1 AND decision = 'approved' ORDER BY tier`, disposalID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []int
	for rows.Next() {
		var t int
		if err := rows.Scan(&t); err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

func (s *Store) actorApprovedDisposal(ctx context.Context, tx pgx.Tx, disposalID uuid.UUID, actor string) (bool, error) {
	if actor == "" {
		return false, nil
	}
	var exists bool
	err := tx.QueryRow(ctx, `
		SELECT EXISTS(SELECT 1 FROM wh_disposal_approvals
		WHERE disposal_id = $1 AND decision = 'approved' AND lower(actor) = lower($2))`, disposalID, actor).Scan(&exists)
	return exists, err
}

func containsTier(xs []int, v int) bool {
	for _, x := range xs {
		if x == v {
			return true
		}
	}
	return false
}

func buildDisposalProgress(required []DisposalApprovalTier, approved []int, complete bool) *DisposalApprovalProgress {
	p := &DisposalApprovalProgress{ApprovedTiers: approved, Complete: complete}
	for _, t := range required {
		p.RequiredTiers = append(p.RequiredTiers, t.Tier)
		if p.NextTier == nil && !containsTier(approved, t.Tier) {
			tier := t.Tier
			p.NextTier = &tier
			p.NextPerm = t.RequiredPerm
		}
	}
	if complete {
		p.NextTier = nil
		p.NextPerm = ""
	}
	return p
}

// ApproveDisposal records the caller's signature for the lowest not-yet-cleared
// required tier and, once all required tiers have signed, executes the disposal.
// hasPerm enforces the tier permission; distinct approvers are required and the
// requester cannot self-approve via the same actor identity twice.
func (s *Store) ApproveDisposal(ctx context.Context, disposalID uuid.UUID, actor string, hasPerm func(string) bool, note string) (models.AssetDisposal, *DisposalApprovalProgress, error) {
	if hasPerm == nil {
		hasPerm = func(string) bool { return false }
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return models.AssetDisposal{}, nil, err
	}
	defer tx.Rollback(ctx)

	var status, requestedBy string
	var value float64
	err = tx.QueryRow(ctx, `SELECT status, disposal_value, requested_by FROM wh_asset_disposals WHERE id = $1 FOR UPDATE`, disposalID).Scan(&status, &value, &requestedBy)
	if errors.Is(err, pgx.ErrNoRows) {
		return models.AssetDisposal{}, nil, ErrNotFound
	}
	if err != nil {
		return models.AssetDisposal{}, nil, err
	}
	if status == "executed" || status == "rejected" {
		return models.AssetDisposal{}, nil, fmt.Errorf("%w: disposal is %s", ErrInvalidArgument, status)
	}
	// Segregation of duties: the requester may not approve their own disposal.
	if sameActor(actor, requestedBy) {
		return models.AssetDisposal{}, nil, fmt.Errorf("%w: the requester cannot approve their own disposal", ErrForbidden)
	}

	tiers, err := s.listDisposalTiersTx(ctx, tx)
	if err != nil {
		return models.AssetDisposal{}, nil, err
	}
	required := requiredDisposalTiers(tiers, value)
	approved, err := s.approvedDisposalTiers(ctx, tx, disposalID)
	if err != nil {
		return models.AssetDisposal{}, nil, err
	}
	if dup, err := s.actorApprovedDisposal(ctx, tx, disposalID, actor); err != nil {
		return models.AssetDisposal{}, nil, err
	} else if dup {
		return models.AssetDisposal{}, nil, fmt.Errorf("%w: %s already approved a tier on this disposal", ErrForbidden, actor)
	}

	var next *DisposalApprovalTier
	for i := range required {
		if !containsTier(approved, required[i].Tier) {
			next = &required[i]
			break
		}
	}

	finalize := false
	if next == nil {
		finalize = true
	} else {
		if !hasPerm(next.RequiredPerm) {
			return models.AssetDisposal{}, nil, fmt.Errorf("%w: approving tier %d requires %s", ErrForbidden, next.Tier, next.RequiredPerm)
		}
		if _, err := tx.Exec(ctx, `
			INSERT INTO wh_disposal_approvals (disposal_id, tier, actor, decision, note)
			VALUES ($1, $2, $3, 'approved', $4)`, disposalID, next.Tier, actor, note); err != nil {
			return models.AssetDisposal{}, nil, err
		}
		approved = append(approved, next.Tier)
		finalize = allDisposalTiersApproved(required, approved)
	}

	if finalize {
		if err := s.executeDisposalTx(ctx, tx, disposalID, nil); err != nil {
			return models.AssetDisposal{}, nil, err
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return models.AssetDisposal{}, nil, err
	}
	d, err := s.getDisposal(ctx, disposalID)
	if err != nil {
		return models.AssetDisposal{}, nil, err
	}
	return d, buildDisposalProgress(required, approved, finalize), nil
}

func allDisposalTiersApproved(required []DisposalApprovalTier, approved []int) bool {
	for _, t := range required {
		if !containsTier(approved, t.Tier) {
			return false
		}
	}
	return true
}

// RejectDisposal rejects a pending disposal. Any holder of a required tier
// permission can reject; the asset is never retired.
func (s *Store) RejectDisposal(ctx context.Context, disposalID uuid.UUID, actor string, hasPerm func(string) bool, note string) (models.AssetDisposal, *DisposalApprovalProgress, error) {
	if hasPerm == nil {
		hasPerm = func(string) bool { return false }
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return models.AssetDisposal{}, nil, err
	}
	defer tx.Rollback(ctx)

	var status string
	var value float64
	err = tx.QueryRow(ctx, `SELECT status, disposal_value FROM wh_asset_disposals WHERE id = $1 FOR UPDATE`, disposalID).Scan(&status, &value)
	if errors.Is(err, pgx.ErrNoRows) {
		return models.AssetDisposal{}, nil, ErrNotFound
	}
	if err != nil {
		return models.AssetDisposal{}, nil, err
	}
	if status == "executed" || status == "rejected" {
		return models.AssetDisposal{}, nil, fmt.Errorf("%w: disposal is %s", ErrInvalidArgument, status)
	}

	tiers, err := s.listDisposalTiersTx(ctx, tx)
	if err != nil {
		return models.AssetDisposal{}, nil, err
	}
	required := requiredDisposalTiers(tiers, value)
	allowed := len(required) == 0
	rejectTier := 0
	for _, t := range required {
		if hasPerm(t.RequiredPerm) {
			allowed = true
			if rejectTier == 0 {
				rejectTier = t.Tier
			}
		}
	}
	if !allowed {
		return models.AssetDisposal{}, nil, fmt.Errorf("%w: rejecting this disposal requires a tier approval permission", ErrForbidden)
	}

	if _, err := tx.Exec(ctx, `
		INSERT INTO wh_disposal_approvals (disposal_id, tier, actor, decision, note)
		VALUES ($1, $2, $3, 'rejected', $4)`, disposalID, rejectTier, actor, note); err != nil {
		return models.AssetDisposal{}, nil, err
	}
	if _, err := tx.Exec(ctx, `UPDATE wh_asset_disposals SET status = 'rejected' WHERE id = $1`, disposalID); err != nil {
		return models.AssetDisposal{}, nil, err
	}
	approved, _ := s.approvedDisposalTiers(ctx, tx, disposalID)
	if err := tx.Commit(ctx); err != nil {
		return models.AssetDisposal{}, nil, err
	}
	d, err := s.getDisposal(ctx, disposalID)
	if err != nil {
		return models.AssetDisposal{}, nil, err
	}
	return d, buildDisposalProgress(required, approved, false), nil
}
