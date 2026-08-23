package store

import (
	"context"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"iag-warehouse/backend/internal/models"
)

// Transaction kinds the item lifecycle gate distinguishes. Obsolete is the only
// status that treats them differently, and it is the reason the gate takes an
// action at all rather than a single transactable flag.
const (
	ItemActionReceive = "receive"
	ItemActionIssue   = "issue"
)

// ItemStatusError is returned when an item's lifecycle status forbids the
// transaction. It carries the SKU and status so the HTTP layer can say which
// item and why without a second query — a refusal naming five line items and
// no reason is a support ticket.
type ItemStatusError struct {
	SKU    string
	Status string
	Action string
}

func (e *ItemStatusError) Error() string {
	verb := "issued"
	if e.Action == ItemActionReceive {
		verb = "received"
	}
	switch e.Status {
	case models.ItemStatusDraft:
		return fmt.Sprintf("%s is still a draft item and cannot be %s until it is approved", e.SKU, verb)
	case models.ItemStatusRestricted:
		return fmt.Sprintf("%s is restricted and cannot be %s without the item-status override permission", e.SKU, verb)
	case models.ItemStatusObsolete:
		return fmt.Sprintf("%s is obsolete and cannot be received; existing stock can still be issued", e.SKU)
	case models.ItemStatusBlocked:
		return fmt.Sprintf("%s is blocked and cannot be %s", e.SKU, verb)
	}
	return fmt.Sprintf("%s cannot be %s in status %q", e.SKU, verb, e.Status)
}

// SetItemLifecycle turns the status gate on or off. Off restores the behaviour
// of every release before migration 026, where status was informational.
func (s *Store) SetItemLifecycle(enabled bool) { s.itemLifecycle = enabled }

// itemStatusAllows applies the rules in migration 026.
//
// The gate lives in the store rather than the handlers on purpose: a status
// that only the HTTP layer honours is a status the event consumers, the
// service-to-service callers and the next handler somebody adds will all walk
// straight past.
func itemStatusAllows(status, action string, allowRestricted bool) bool {
	switch status {
	case models.ItemStatusActive:
		return true
	case models.ItemStatusRestricted:
		return allowRestricted
	case models.ItemStatusObsolete:
		// Receiving more of a part we have decided to stop using is the thing
		// worth refusing. Issuing what is already on the shelf is how the
		// balance reaches zero.
		return action == ItemActionIssue
	case models.ItemStatusDraft, models.ItemStatusBlocked:
		return false
	}
	// An unrecognised status is a data problem, not a licence. Refuse, so it
	// surfaces as a refusal naming the status rather than as silent movement.
	return false
}

// assertItemsTransactable refuses the whole document if any line names an item
// its status forbids.
//
// All-or-nothing is deliberate. Posting the lines that happen to be allowed and
// dropping the rest would leave a receipt that silently disagrees with the
// delivery note in front of the storekeeper.
//
// One query for the whole line set, not one per line: a receipt with forty
// lines should cost one round trip.
//
// When the caller's override lets a restricted item through, that is logged in
// the same transaction as the movement it permitted. Recording it afterwards
// from the handler would let a rolled-back issue leave an override entry for a
// movement that never happened — and an exception report with phantom rows in
// it stops being read.
func (s *Store) assertItemsTransactable(
	ctx context.Context,
	tx pgx.Tx,
	itemIDs []uuid.UUID,
	action string,
	allowRestricted bool,
	actor *uuid.UUID,
) error {
	if !s.itemLifecycle || len(itemIDs) == 0 {
		return nil
	}

	seen := make(map[uuid.UUID]struct{}, len(itemIDs))
	unique := make([]uuid.UUID, 0, len(itemIDs))
	for _, id := range itemIDs {
		if _, dup := seen[id]; dup || id == uuid.Nil {
			continue
		}
		seen[id] = struct{}{}
		unique = append(unique, id)
	}
	if len(unique) == 0 {
		return nil
	}

	rows, err := tx.Query(ctx, `SELECT sku, status FROM wh_items WHERE id = ANY($1)`, unique)
	if err != nil {
		return err
	}
	defer rows.Close()

	var refusals []*ItemStatusError
	var overridden []string
	for rows.Next() {
		var sku, status string
		if err := rows.Scan(&sku, &status); err != nil {
			return err
		}
		if !itemStatusAllows(status, action, allowRestricted) {
			refusals = append(refusals, &ItemStatusError{SKU: sku, Status: status, Action: action})
			continue
		}
		if status == models.ItemStatusRestricted {
			overridden = append(overridden, sku)
		}
	}
	if err := rows.Err(); err != nil {
		return err
	}
	if len(refusals) == 0 {
		return s.recordItemStatusOverridesTx(ctx, tx, overridden, action, actor)
	}
	if len(refusals) == 1 {
		return refusals[0]
	}
	// Report every offending line at once. Refusing them one delivery at a time
	// makes a forty-line receipt a forty-attempt conversation.
	parts := make([]string, 0, len(refusals))
	for _, r := range refusals {
		parts = append(parts, r.Error())
	}
	return &ItemStatusError{
		SKU:    strings.Join(parts, "; "),
		Status: refusals[0].Status,
		Action: action,
	}
}

// recordItemStatusOverridesTx logs each restricted item the caller's override
// permission let through, one row per SKU so the exception report names the
// part rather than the document.
func (s *Store) recordItemStatusOverridesTx(
	ctx context.Context,
	tx pgx.Tx,
	skus []string,
	action string,
	actor *uuid.UUID,
) error {
	for _, sku := range skus {
		if _, err := tx.Exec(ctx, `
			INSERT INTO wh_control_overrides
				(kind, subject, from_state, to_state, reason, ref_type, permission, actor_id)
			VALUES ('item_status', $1, 'restricted', 'transacted', $2, $3, 'warehouse.override_item_status', $4)`,
			sku,
			"restricted item "+action+"d under override",
			action,
			actor,
		); err != nil {
			return err
		}
	}
	return nil
}

// SetItemStatus moves an item through its lifecycle, rejecting an unknown state
// before it reaches the CHECK constraint so the caller gets the valid set back.
func (s *Store) SetItemStatus(ctx context.Context, id uuid.UUID, status string) (models.Item, error) {
	if !models.ValidItemStatus(status) {
		return models.Item{}, fmt.Errorf("%w: status must be one of %s",
			ErrInvalidArgument, strings.Join(models.ItemStatuses, ", "))
	}
	var item models.Item
	err := s.pool.QueryRow(ctx, `
		UPDATE wh_items SET status = $2, updated_at = NOW()
		WHERE id = $1
		RETURNING id, sku, name, material_class, tracking_mode, uom, min_qty, max_qty, status, attrs, created_at, updated_at`,
		id, status,
	).Scan(&item.ID, &item.SKU, &item.Name, &item.MaterialClass, &item.TrackingMode, &item.UOM,
		&item.MinQty, &item.MaxQty, &item.Status, &item.Attrs, &item.CreatedAt, &item.UpdatedAt)
	if err == pgx.ErrNoRows {
		return item, ErrNotFound
	}
	return item, err
}
