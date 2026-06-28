# Field-intake coffee sacks → warehouse stock

How the **Field & Farm** field-agent app maps RFID-tagged coffee sacks
(`{rfid, kg, farmer, bean, source, condition}`) onto warehouse stock. This
replaces the standalone apiababy `/stock` endpoint.

## Item of record

Migration `009_field_intake_sack.sql` seeds:

| SKU | Name | material_class | tracking_mode | uom |
|-----|------|----------------|---------------|-----|
| `RM-FIELD-SACK` | Field Coffee Sack (parchment/cherry) | `raw_material` | `lot` | `kg` |

Lot tracking (not `serial`) is deliberate: each physical sack carries its **RFID
as the `lot_key`**, while keeping a variable **kg** quantity. Per-sack provenance
rides in `attrs`.

## Receiving a sack (recommended flow)

The app records a sack as a one-line **receipt** against the receiving bin, then
posts it to stock. Through the gateway: `…/api/v1/warehouse/api/v1/...`.

```http
POST /api/v1/receipts            # requires warehouse.add_receipt
{
  "receipt_type": "field_intake",
  "lines": [{
    "item_id": "<RM-FIELD-SACK id>",   # GET /api/v1/items?sku=RM-FIELD-SACK
    "qty": 62,                          # kg on the scale
    "uom": "kg",
    "bin_code": "RCV-01",               # e.g. MBALE-MILL receiving
    "lot_key": "E2801160600002...",     # the RFID tag
    "batch_business_id": "BAT-2026-...",# optional: SCM intake batch link
    "attrs": { "farmer": "FRM-014", "bean": "arabica",
               "source": "farm-gate", "condition": "good" }
  }]
}

POST /api/v1/receipts/{id}/post  # requires warehouse.post_receipt → updates wh_stock_balances
```

Query a farmer's sacks / current location: `GET /api/v1/items/{id}/balances`
(returns rows keyed by `lot_key`/`bin`), and `GET /api/v1/movements?...` for the
audit trail.

## Permissions

Field agents need (granted alongside their SCM "Field Operations" perms — see
the SCM seed): `warehouse.view_item`, `warehouse.view_stock`,
`warehouse.add_receipt`, `warehouse.post_receipt`. `warehouse.add_item` is only
needed if the app ever creates new SKUs (it should not — `RM-FIELD-SACK` is
seeded).

## Notes / gaps

- No RFID-hardware integration in warehouse — `lot_key` is the scanned string;
  the app owns the scan.
- `condition` (good/damaged) lives in `attrs`; the richer `wh_assets.condition`
  enum is for equipment, not coffee sacks.
- "Damaged"/hold handling uses `POST /api/v1/adjustments` (status change), not a
  per-sack delete (apiababy's `DELETE /stock/{id}` has no warehouse equivalent).
