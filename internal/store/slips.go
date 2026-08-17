package store

import (
	"context"
	"crypto/rand"
	"encoding/base32"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"iag-warehouse/backend/internal/events"
	"iag-warehouse/backend/internal/models"
)

// Equipment handover slips and cargo gate passes.
//
// The document's life is: raised as a draft, authorised by somebody other than
// whoever raised it, verified and released at the gate, and — if it is
// returnable — outstanding until it comes back. Each transition writes a
// wh_slip_events row, because the value of a gate pass is almost entirely in
// being able to say afterwards who let what out and on whose authority.

const slipCols = `s.id, s.slip_no, s.slip_type, s.status, s.purpose, s.notes, s.facility_id,
	s.issued_to_name, s.issued_to_id, s.dept, s.from_custodian, s.to_custodian,
	s.driver_name, s.driver_id_no, s.vehicle_reg, s.transporter, s.destination,
	s.returnable, s.return_by, s.returned_at, s.returned_condition,
	s.requested_by, s.requested_name, s.authorized_by, s.authorized_name, s.authorized_at, s.reject_reason,
	s.gate_name, s.gate_verified_by, s.gate_verified_name, s.gate_verified_at, s.gate_notes,
	s.issue_id, s.pick_list_id, s.hu_id, s.ref_type, s.ref_id,
	s.attrs, s.created_by, s.created_at, s.updated_at,
	COALESCE(f.code, ''), COALESCE(f.name, ''), COALESCE(h.lpn, ''),
	(s.returnable AND s.return_by IS NOT NULL AND s.return_by < CURRENT_DATE
	 AND s.status IN ('issued', 'released')) AS overdue`

const slipFrom = `
	FROM wh_slips s
	LEFT JOIN wh_facilities f ON f.id = s.facility_id
	LEFT JOIN wh_handling_units h ON h.id = s.hu_id`

func scanSlip(row pgx.Row) (models.Slip, error) {
	var s models.Slip
	err := row.Scan(&s.ID, &s.SlipNo, &s.SlipType, &s.Status, &s.Purpose, &s.Notes, &s.FacilityID,
		&s.IssuedToName, &s.IssuedToID, &s.Dept, &s.FromCustodian, &s.ToCustodian,
		&s.DriverName, &s.DriverIDNo, &s.VehicleReg, &s.Transporter, &s.Destination,
		&s.Returnable, &s.ReturnBy, &s.ReturnedAt, &s.ReturnedCondition,
		&s.RequestedBy, &s.RequestedName, &s.AuthorizedBy, &s.AuthorizedName, &s.AuthorizedAt, &s.RejectReason,
		&s.GateName, &s.GateVerifiedBy, &s.GateVerifiedName, &s.GateVerifiedAt, &s.GateNotes,
		&s.IssueID, &s.PickListID, &s.HUID, &s.RefType, &s.RefID,
		&s.Attrs, &s.CreatedBy, &s.CreatedAt, &s.UpdatedAt,
		&s.FacilityCode, &s.FacilityName, &s.HULPN, &s.Overdue)
	return s, err
}

type SlipLineInput struct {
	ItemID       *uuid.UUID
	AssetID      *uuid.UUID
	Description  string
	Qty          float64
	UOM          string
	SerialNo     string
	LotKey       string
	ConditionOut string
}

type CreateSlipInput struct {
	SlipType   string
	Purpose    string
	Notes      string
	FacilityID *uuid.UUID

	IssuedToName  string
	IssuedToID    *uuid.UUID
	Dept          string
	FromCustodian string
	ToCustodian   string

	DriverName  string
	DriverIDNo  string
	VehicleReg  string
	Transporter string
	Destination string

	Returnable bool
	ReturnBy   *time.Time

	IssueID    *uuid.UUID
	PickListID *uuid.UUID
	HUID       *uuid.UUID
	RefType    string
	RefID      string

	RequestedBy   *uuid.UUID
	RequestedName string
	Lines         []SlipLineInput
	CreatedBy     *uuid.UUID
}

// CreateSlip raises a draft. A draft has no number and no verification token —
// it is deliberately worthless at the gate until somebody authorises it.
func (s *Store) CreateSlip(ctx context.Context, in CreateSlipInput) (models.Slip, error) {
	switch in.SlipType {
	case models.SlipEquipmentHandover, models.SlipCargoGatePass:
	default:
		return models.Slip{}, fmt.Errorf("%w: slip_type must be equipment_handover or cargo_gate_pass", ErrInvalidArgument)
	}
	if len(in.Lines) == 0 {
		return models.Slip{}, fmt.Errorf("%w: a slip must list what is leaving", ErrInvalidArgument)
	}
	if in.SlipType == models.SlipCargoGatePass && strings.TrimSpace(in.DriverName) == "" && strings.TrimSpace(in.VehicleReg) == "" {
		return models.Slip{}, fmt.Errorf("%w: a cargo gate pass needs a driver or a vehicle registration", ErrInvalidArgument)
	}
	if in.Returnable && in.ReturnBy == nil {
		return models.Slip{}, fmt.Errorf("%w: a returnable slip needs a return_by date", ErrInvalidArgument)
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return models.Slip{}, err
	}
	defer tx.Rollback(ctx)

	var id uuid.UUID
	err = tx.QueryRow(ctx, `
		INSERT INTO wh_slips (slip_type, purpose, notes, facility_id, issued_to_name, issued_to_id, dept,
			from_custodian, to_custodian, driver_name, driver_id_no, vehicle_reg, transporter, destination,
			returnable, return_by, issue_id, pick_list_id, hu_id, ref_type, ref_id,
			requested_by, requested_name, created_by)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18,$19,$20,$21,$22,$23,$24)
		RETURNING id`,
		in.SlipType, in.Purpose, in.Notes, in.FacilityID, in.IssuedToName, in.IssuedToID, in.Dept,
		in.FromCustodian, in.ToCustodian, in.DriverName, in.DriverIDNo, in.VehicleReg, in.Transporter, in.Destination,
		in.Returnable, in.ReturnBy, in.IssueID, in.PickListID, in.HUID, in.RefType, in.RefID,
		in.RequestedBy, in.RequestedName, in.CreatedBy).Scan(&id)
	if err != nil {
		return models.Slip{}, err
	}

	for _, l := range in.Lines {
		desc := strings.TrimSpace(l.Description)
		if desc == "" {
			return models.Slip{}, fmt.Errorf("%w: every slip line needs a description", ErrInvalidArgument)
		}
		if l.Qty <= 0 {
			l.Qty = 1
		}
		if l.UOM == "" {
			l.UOM = "ea"
		}
		if _, err := tx.Exec(ctx, `
			INSERT INTO wh_slip_lines (slip_id, item_id, asset_id, description, qty, uom, serial_no, lot_key, condition_out)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9)`,
			id, l.ItemID, l.AssetID, desc, l.Qty, l.UOM, l.SerialNo, l.LotKey, l.ConditionOut); err != nil {
			return models.Slip{}, err
		}
	}

	if err := s.addSlipEventTx(ctx, tx, id, "raised", in.CreatedBy, in.RequestedName, ""); err != nil {
		return models.Slip{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return models.Slip{}, err
	}
	return s.GetSlip(ctx, id)
}

func (s *Store) addSlipEventTx(ctx context.Context, tx pgx.Tx, slipID uuid.UUID, event string, actorID *uuid.UUID, actorName, notes string) error {
	_, err := tx.Exec(ctx, `
		INSERT INTO wh_slip_events (slip_id, event, actor_id, actor_name, notes)
		VALUES ($1,$2,$3,$4,$5)`, slipID, event, actorID, actorName, notes)
	return err
}

// GetSlip returns the slip with its lines and its history. The verification
// token is included — this is the detail view an authorised user opens to print,
// and printing is exactly what the token is for.
func (s *Store) GetSlip(ctx context.Context, id uuid.UUID) (models.Slip, error) {
	slip, err := scanSlip(s.pool.QueryRow(ctx, `SELECT `+slipCols+slipFrom+` WHERE s.id = $1`, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return slip, ErrNotFound
	}
	if err != nil {
		return slip, err
	}
	if err := s.pool.QueryRow(ctx, `SELECT verify_token FROM wh_slips WHERE id = $1`, id).Scan(&slip.VerifyToken); err != nil {
		return slip, err
	}
	return s.withSlipDetail(ctx, slip)
}

func (s *Store) withSlipDetail(ctx context.Context, slip models.Slip) (models.Slip, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT l.id, l.slip_id, l.item_id, l.asset_id, l.description, l.qty, l.uom, l.serial_no, l.lot_key,
			l.condition_out, l.condition_in, l.returned_qty, l.attrs,
			COALESCE(i.sku, ''), COALESCE(a.asset_tag, '')
		FROM wh_slip_lines l
		LEFT JOIN wh_items i ON i.id = l.item_id
		LEFT JOIN wh_assets a ON a.id = l.asset_id
		WHERE l.slip_id = $1 ORDER BY l.description`, slip.ID)
	if err != nil {
		return slip, err
	}
	slip.Lines = []models.SlipLine{}
	for rows.Next() {
		var l models.SlipLine
		if err := rows.Scan(&l.ID, &l.SlipID, &l.ItemID, &l.AssetID, &l.Description, &l.Qty, &l.UOM,
			&l.SerialNo, &l.LotKey, &l.ConditionOut, &l.ConditionIn, &l.ReturnedQty, &l.Attrs,
			&l.ItemSKU, &l.AssetTag); err != nil {
			rows.Close()
			return slip, err
		}
		slip.Lines = append(slip.Lines, l)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return slip, err
	}

	erows, err := s.pool.Query(ctx, `
		SELECT id, slip_id, event, actor_id, actor_name, notes, created_at
		FROM wh_slip_events WHERE slip_id = $1 ORDER BY created_at`, slip.ID)
	if err != nil {
		return slip, err
	}
	defer erows.Close()
	slip.Events = []models.SlipEvent{}
	for erows.Next() {
		var e models.SlipEvent
		if err := erows.Scan(&e.ID, &e.SlipID, &e.Event, &e.ActorID, &e.ActorName, &e.Notes, &e.CreatedAt); err != nil {
			return slip, err
		}
		slip.Events = append(slip.Events, e)
	}
	return slip, erows.Err()
}

// SlipFilter narrows a slip listing. Overdue is the one that matters
// operationally: it answers "what is still out that should be back".
type SlipFilter struct {
	SlipType string
	Status   string
	Overdue  bool
	Limit    int
}

// ListSlips returns slips without their verification tokens. A directory of
// slips must not double as a directory of gate keys.
func (s *Store) ListSlips(ctx context.Context, f SlipFilter) ([]models.Slip, error) {
	if f.Limit <= 0 || f.Limit > 200 {
		f.Limit = 100
	}
	query := `SELECT ` + slipCols + slipFrom + ` WHERE TRUE`
	args := []any{}
	if f.SlipType != "" {
		args = append(args, f.SlipType)
		query += fmt.Sprintf(` AND s.slip_type = $%d`, len(args))
	}
	if f.Status != "" {
		args = append(args, f.Status)
		query += fmt.Sprintf(` AND s.status = $%d`, len(args))
	}
	if f.Overdue {
		query += ` AND s.returnable AND s.return_by IS NOT NULL AND s.return_by < CURRENT_DATE
			AND s.status IN ('issued', 'released')`
	}
	args = append(args, f.Limit)
	query += fmt.Sprintf(` ORDER BY s.created_at DESC LIMIT $%d`, len(args))

	rows, err := s.pool.Query(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []models.Slip{}
	for rows.Next() {
		slip, err := scanSlip(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, slip)
	}
	return out, rows.Err()
}

// newVerifyToken produces the value the printed barcode encodes. Base32 without
// padding keeps it inside the Code 39 alphabet, so it can actually be printed as
// a scannable symbol, and 80 bits of entropy makes forging one by guessing not
// worth attempting.
func newVerifyToken() (string, error) {
	b := make([]byte, 10)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(b), nil
}

// AuthorizeSlip signs the document off, giving it its number and its token.
//
// requireSeparateAuthorizer is the control that makes the whole thing mean
// something: the person who wrote out what is leaving cannot also be the person
// who says it may. Without it a gate pass is a note somebody wrote themselves.
func (s *Store) AuthorizeSlip(ctx context.Context, id uuid.UUID, actorID *uuid.UUID, actorName string, requireSeparateAuthorizer bool) (models.Slip, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return models.Slip{}, err
	}
	defer tx.Rollback(ctx)

	var status, slipType string
	var createdBy, requestedBy *uuid.UUID
	err = tx.QueryRow(ctx,
		`SELECT status, slip_type, created_by, requested_by FROM wh_slips WHERE id = $1 FOR UPDATE`, id,
	).Scan(&status, &slipType, &createdBy, &requestedBy)
	if errors.Is(err, pgx.ErrNoRows) {
		return models.Slip{}, ErrNotFound
	}
	if err != nil {
		return models.Slip{}, err
	}
	if status == models.SlipIssued {
		return s.GetSlip(ctx, id)
	}
	if status != models.SlipDraft {
		return models.Slip{}, fmt.Errorf("%w: only a draft can be authorised (this one is %s)", ErrConflict, status)
	}
	if requireSeparateAuthorizer && actorID != nil {
		if (createdBy != nil && *createdBy == *actorID) || (requestedBy != nil && *requestedBy == *actorID) {
			return models.Slip{}, fmt.Errorf("%w: a slip must be authorised by someone other than the person who raised it", ErrForbidden)
		}
	}

	prefix, series := "GP", "gatepass"
	if slipType == models.SlipEquipmentHandover {
		prefix, series = "EH", "handover"
	}
	slipNo, err := nextDocumentNumber(ctx, tx, series, prefix)
	if err != nil {
		return models.Slip{}, err
	}
	token, err := newVerifyToken()
	if err != nil {
		return models.Slip{}, err
	}

	if _, err := tx.Exec(ctx, `
		UPDATE wh_slips SET status = 'issued', slip_no = $2, verify_token = $3,
			authorized_by = $4, authorized_name = $5, authorized_at = NOW(), updated_at = NOW()
		WHERE id = $1`, id, slipNo, token, actorID, actorName); err != nil {
		return models.Slip{}, err
	}
	if err := s.addSlipEventTx(ctx, tx, id, "authorised", actorID, actorName, slipNo); err != nil {
		return models.Slip{}, err
	}
	if s.bus != nil && s.bus.Enabled() {
		if err := s.bus.PublishTx(ctx, tx, events.TypeSlipIssued, map[string]any{
			"slip_id": id.String(), "slip_no": slipNo, "slip_type": slipType,
		}, id.String()); err != nil {
			return models.Slip{}, err
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return models.Slip{}, err
	}
	return s.GetSlip(ctx, id)
}

func (s *Store) RejectSlip(ctx context.Context, id uuid.UUID, reason string, actorID *uuid.UUID, actorName string) (models.Slip, error) {
	reason = strings.TrimSpace(reason)
	if reason == "" {
		return models.Slip{}, fmt.Errorf("%w: a rejection needs a reason", ErrInvalidArgument)
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return models.Slip{}, err
	}
	defer tx.Rollback(ctx)

	tag, err := tx.Exec(ctx, `
		UPDATE wh_slips SET status = 'rejected', reject_reason = $2, updated_at = NOW()
		WHERE id = $1 AND status = 'draft'`, id, reason)
	if err != nil {
		return models.Slip{}, err
	}
	if tag.RowsAffected() == 0 {
		return models.Slip{}, ErrConflict
	}
	if err := s.addSlipEventTx(ctx, tx, id, "rejected", actorID, actorName, reason); err != nil {
		return models.Slip{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return models.Slip{}, err
	}
	return s.GetSlip(ctx, id)
}

// VerifySlipToken is what the guard's terminal calls when it scans the barcode.
// It is a read: verifying does not release anything, because a guard checking a
// slip and a guard letting the lorry through are two different decisions and
// only the second should be recorded as having happened.
//
// An unknown token returns not-found rather than any detail about why.
func (s *Store) VerifySlipToken(ctx context.Context, token string) (models.Slip, error) {
	token = strings.ToUpper(strings.TrimSpace(token))
	if token == "" {
		return models.Slip{}, ErrNotFound
	}
	slip, err := scanSlip(s.pool.QueryRow(ctx, `SELECT `+slipCols+slipFrom+` WHERE s.verify_token = $1`, token))
	if errors.Is(err, pgx.ErrNoRows) {
		return models.Slip{}, ErrNotFound
	}
	if err != nil {
		return models.Slip{}, err
	}
	return s.withSlipDetail(ctx, slip)
}

// ReleaseSlip records that the goods physically left, and who at the gate let
// them. Only an issued slip can be released — a draft, a rejected slip or one
// already released must not pass twice on the same paper.
func (s *Store) ReleaseSlip(ctx context.Context, id uuid.UUID, gateName, notes string, actorID *uuid.UUID, actorName string) (models.Slip, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return models.Slip{}, err
	}
	defer tx.Rollback(ctx)

	var status string
	var slipNo *string
	err = tx.QueryRow(ctx, `SELECT status, slip_no FROM wh_slips WHERE id = $1 FOR UPDATE`, id).Scan(&status, &slipNo)
	if errors.Is(err, pgx.ErrNoRows) {
		return models.Slip{}, ErrNotFound
	}
	if err != nil {
		return models.Slip{}, err
	}
	if status == models.SlipReleased {
		return s.GetSlip(ctx, id)
	}
	if status != models.SlipIssued {
		return models.Slip{}, fmt.Errorf("%w: only an authorised slip can be released at the gate (this one is %s)", ErrConflict, status)
	}

	if _, err := tx.Exec(ctx, `
		UPDATE wh_slips SET status = 'released', gate_name = $2, gate_notes = $3,
			gate_verified_by = $4, gate_verified_name = $5, gate_verified_at = NOW(), updated_at = NOW()
		WHERE id = $1`, id, gateName, notes, actorID, actorName); err != nil {
		return models.Slip{}, err
	}
	if err := s.addSlipEventTx(ctx, tx, id, "released", actorID, actorName, gateName); err != nil {
		return models.Slip{}, err
	}
	if s.bus != nil && s.bus.Enabled() {
		key := id.String()
		if slipNo != nil {
			key = *slipNo
		}
		if err := s.bus.PublishTx(ctx, tx, events.TypeSlipReleased, map[string]any{
			"slip_id": id.String(), "slip_no": slipNo, "gate": gateName,
		}, key); err != nil {
			return models.Slip{}, err
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return models.Slip{}, err
	}
	return s.GetSlip(ctx, id)
}

// SlipReturnLine records what came back on one line of a returnable slip.
type SlipReturnLine struct {
	LineID      uuid.UUID
	ReturnedQty float64
	ConditionIn string
}

// ReturnSlip closes the loop on a returnable slip. A slip whose lines all came
// back is 'returned'; one where something is still missing stays open so it
// keeps showing up as outstanding, which is the only way anybody ever chases it.
func (s *Store) ReturnSlip(ctx context.Context, id uuid.UUID, lines []SlipReturnLine, condition string, actorID *uuid.UUID, actorName string) (models.Slip, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return models.Slip{}, err
	}
	defer tx.Rollback(ctx)

	var status string
	var returnable bool
	err = tx.QueryRow(ctx, `SELECT status, returnable FROM wh_slips WHERE id = $1 FOR UPDATE`, id).Scan(&status, &returnable)
	if errors.Is(err, pgx.ErrNoRows) {
		return models.Slip{}, ErrNotFound
	}
	if err != nil {
		return models.Slip{}, err
	}
	if !returnable {
		return models.Slip{}, fmt.Errorf("%w: this slip was not issued as returnable", ErrConflict)
	}
	if status != models.SlipIssued && status != models.SlipReleased && status != models.SlipReturned {
		return models.Slip{}, fmt.Errorf("%w: a %s slip cannot take a return", ErrConflict, status)
	}

	for _, l := range lines {
		if l.ReturnedQty < 0 {
			return models.Slip{}, fmt.Errorf("%w: returned_qty cannot be negative", ErrInvalidArgument)
		}
		tag, err := tx.Exec(ctx, `
			UPDATE wh_slip_lines SET returned_qty = LEAST($3, qty), condition_in = COALESCE(NULLIF($4, ''), condition_in)
			WHERE id = $2 AND slip_id = $1`, id, l.LineID, l.ReturnedQty, l.ConditionIn)
		if err != nil {
			return models.Slip{}, err
		}
		if tag.RowsAffected() == 0 {
			return models.Slip{}, ErrNotFound
		}
	}

	var outstanding int
	if err := tx.QueryRow(ctx,
		`SELECT COUNT(*) FROM wh_slip_lines WHERE slip_id = $1 AND returned_qty < qty`, id).Scan(&outstanding); err != nil {
		return models.Slip{}, err
	}

	newStatus := models.SlipReleased
	event := "part-returned"
	if outstanding == 0 {
		newStatus = models.SlipReturned
		event = "returned"
	}
	if _, err := tx.Exec(ctx, `
		UPDATE wh_slips SET status = $2, returned_condition = COALESCE(NULLIF($3, ''), returned_condition),
			returned_at = CASE WHEN $2 = 'returned' THEN NOW() ELSE returned_at END, updated_at = NOW()
		WHERE id = $1`, id, newStatus, condition); err != nil {
		return models.Slip{}, err
	}
	if err := s.addSlipEventTx(ctx, tx, id, event, actorID, actorName, condition); err != nil {
		return models.Slip{}, err
	}
	if outstanding == 0 && s.bus != nil && s.bus.Enabled() {
		if err := s.bus.PublishTx(ctx, tx, events.TypeSlipReturned, map[string]any{
			"slip_id": id.String(),
		}, id.String()); err != nil {
			return models.Slip{}, err
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return models.Slip{}, err
	}
	return s.GetSlip(ctx, id)
}

// CloseSlip retires a slip that needs no further action — a non-returnable pass
// whose goods have gone, or a returnable one written off after investigation.
func (s *Store) CloseSlip(ctx context.Context, id uuid.UUID, notes string, actorID *uuid.UUID, actorName string) (models.Slip, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return models.Slip{}, err
	}
	defer tx.Rollback(ctx)

	tag, err := tx.Exec(ctx, `
		UPDATE wh_slips SET status = 'closed', updated_at = NOW()
		WHERE id = $1 AND status IN ('issued', 'released', 'returned')`, id)
	if err != nil {
		return models.Slip{}, err
	}
	if tag.RowsAffected() == 0 {
		return models.Slip{}, ErrConflict
	}
	if err := s.addSlipEventTx(ctx, tx, id, "closed", actorID, actorName, notes); err != nil {
		return models.Slip{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return models.Slip{}, err
	}
	return s.GetSlip(ctx, id)
}

func (s *Store) CancelSlip(ctx context.Context, id uuid.UUID, reason string, actorID *uuid.UUID, actorName string) (models.Slip, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return models.Slip{}, err
	}
	defer tx.Rollback(ctx)

	// A released slip cannot be cancelled: the goods are already outside the
	// fence, and pretending otherwise would erase the only record of that.
	tag, err := tx.Exec(ctx, `
		UPDATE wh_slips SET status = 'cancelled', reject_reason = $2, updated_at = NOW()
		WHERE id = $1 AND status IN ('draft', 'issued')`, id, reason)
	if err != nil {
		return models.Slip{}, err
	}
	if tag.RowsAffected() == 0 {
		return models.Slip{}, ErrConflict
	}
	if err := s.addSlipEventTx(ctx, tx, id, "cancelled", actorID, actorName, reason); err != nil {
		return models.Slip{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return models.Slip{}, err
	}
	return s.GetSlip(ctx, id)
}

// ListOverdueSlips is the chase list: returnable slips past their date that are
// still out. It is what the overdue job reads to raise alerts.
func (s *Store) ListOverdueSlips(ctx context.Context) ([]models.Slip, error) {
	return s.ListSlips(ctx, SlipFilter{Overdue: true, Limit: 200})
}
