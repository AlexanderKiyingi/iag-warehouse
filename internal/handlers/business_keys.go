package handlers

import (
	"context"
	"errors"
	"strings"

	"github.com/google/uuid"

	"iag-warehouse/backend/internal/store"
)

// Resolving a write by business key.
//
// Every write endpoint here originally took `item_id` as a UUID. That is the
// right key for a service-to-service caller, which already holds the id it is
// writing about — but it is unusable for a person filling in a form, and for
// any client that renders a flat record. Such a client holds the SKU, because
// that is what it displays; it has never seen the id.
//
// The consequence was not a clumsy API, it was a silent capability gap: the
// screens for receiving, transferring, adjusting and counting stock could all
// be read and none of them could be written, so they were wired read-only and
// the write half of the warehouse was unreachable from a UI.
//
// So every write path that takes an id now also takes the business key beside
// it. The id still wins when both are sent — a caller that knows the id is
// being precise, and should not have that overridden by a stale SKU.

// errNoItemKey is returned when a line or body identifies no item at all.
var errNoItemKey = errors.New("item_id or item_sku is required")

// resolveItemID turns whichever of the two keys the caller sent into an item id.
//
// A SKU that matches nothing is a client error, not a server one — it is the
// same class of mistake as a malformed UUID — so it comes back as
// store.ErrNotFound for the handler to render as a 400 naming the SKU.
func (a *API) resolveItemID(ctx context.Context, itemID, itemSKU string) (uuid.UUID, error) {
	if id := strings.TrimSpace(itemID); id != "" {
		parsed, err := uuid.Parse(id)
		if err != nil {
			return uuid.Nil, errors.New("invalid item_id")
		}
		return parsed, nil
	}
	sku := strings.TrimSpace(itemSKU)
	if sku == "" {
		return uuid.Nil, errNoItemKey
	}
	id, err := a.Store.GetItemIDBySKU(ctx, sku)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return uuid.Nil, errors.New("unknown item_sku: " + sku)
		}
		return uuid.Nil, err
	}
	return id, nil
}

// resolveBinCode picks the bin a stock write lands in.
//
// A flat-record client knows a location by its facility, not by a bin — the
// form field says "Warehouse / location". When only a facility is given, the
// write goes to that facility's receiving bin, which is the same default
// directed putaway would resolve to. An explicit bin_code always wins.
func (a *API) resolveBinCode(ctx context.Context, binCode, facilityCode string) (string, error) {
	if code := strings.TrimSpace(binCode); code != "" {
		return code, nil
	}
	facility := strings.TrimSpace(facilityCode)
	if facility == "" {
		return "", errors.New("bin_code or facility_code is required")
	}
	code, err := a.Store.DefaultBinCodeForFacility(ctx, facility)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return "", errors.New("no bin available at facility: " + facility)
		}
		return "", err
	}
	return code, nil
}
