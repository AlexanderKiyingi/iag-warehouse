# Perpetual inventory — warehouse event-emitter spec

Cross-service spec for perpetual inventory GL: **iag-warehouse** emits *valued*
stock-movement events, **iag-finance** consumes them to book Cost of Goods Sold
and the inventory control account, and **iag-procurement** nets GR/IR against
supplier bills. Companion to `iag-finance/docs/GAP_REMEDIATION_ROADMAP.md`
("Blocked 3") and `iag-finance/docs/EVENT_CONTRACT.md`.

## Current state (grounded)
- Warehouse already emits **`warehouse.movement.posted`** (`internal/events/bus.go:33`,
  `TypeMovementPosted`) from `internal/inventory/bridge.go` (`EmitMovementPosted`),
  called in `internal/store/stock.go:288` after a stock movement. Topic
  **`iag.operations`** (`bus.go:22`). An outbox exists (`internal/outbox`), and
  the bus supports transactional enqueue via `PublishTx` (`bus.go:113`).
- **The movement payload carries no cost** — `MovementPayload` (`bridge.go`) has
  `qty`, `item_id`, `sku`, `movement_type`, lot/serial, bins; no unit/total cost.
- **No costing engine exists anywhere.** Warehouse items have no cost field
  (`internal/models/models.go` — only `cost_center`). Receipt lines
  (`internal/handlers/receipts.go`) capture no unit cost. `iag-inventory` is an
  auth/RBAC skeleton (no cost/valuation model, no consumer, no outbox).
- Finance has `1400 Inventory`, `2150 GR/IR Clearing`, and now `5000 COGS`
  (finance migration 040) seeded; its `internal/consumer` can consume topics.

## The one architectural decision: where does weighted-average cost live?
- **Option A (recommended, MVP):** warehouse owns moving-average cost. Receipts
  capture `unit_cost` (from the PO/GRN); warehouse maintains `avg_cost` per item
  and values issues at it. The movement event is emitted *valued*. Minimal new
  infra — reuses the existing movement pipeline + outbox. iag-inventory stays a
  read/reporting layer.
- **Option B (later):** stand up `iag-inventory` as the costing service — it
  consumes raw movements + procurement costs, maintains WAC, and emits valued
  movements. Cleaner separation but a large greenfield build.

Recommendation: **A now, B if valuation logic grows.** Rest of this spec assumes A.

## Event contract — extend `warehouse.movement.posted`
Add cost fields (all base-currency decimal strings; `avg_cost_after` is post-move):
```json
{
  "movement_id": "uuid",            // idempotency key
  "movement_type": "receipt|issue|adjustment|transfer",
  "item_id": "uuid", "sku": "…",
  "qty": 12.0,
  "unit_cost": "1500.00",           // receipt: PO cost; issue: avg cost used
  "total_cost": "18000.00",         // signed valuation delta (+in / -out)
  "avg_cost_after": "1487.50",
  "occurred_at": "2026-07-02T…Z",
  "ref": "GRN-1001|ISSUE-55|…"      // source doc for finance memo + audit
}
```
Transfers between bins are cost-neutral (no finance event needed); emit with
`total_cost:"0"` or skip. Keep the existing fields for iag-inventory consumers.

## Warehouse changes (emitter side)
1. **Schema:** add `unit_cost NUMERIC(18,4)` to receipt lines; add
   `avg_cost NUMERIC(18,4) NOT NULL DEFAULT 0` to the item table.
2. **Receipt:** persist `unit_cost` per line; recompute item moving average
   `avg_cost = (on_hand*avg_cost + qty*unit_cost) / (on_hand+qty)`.
3. **Issue/pick/production-consume:** value the movement at the current
   `avg_cost`; populate `unit_cost`/`total_cost`/`avg_cost_after` in
   `MovementPayload`.
4. **Emit transactionally:** switch `EmitMovementPosted` (`stock.go:288`) from
   `bus.Publish` to `bus.PublishTx` inside the stock-write tx so the event and
   the stock change commit atomically (exactly-once via the outbox).
5. Feature-flag the cost fields (`INVENTORY_COSTING_ENABLED`) so the change is
   dark until finance's consumer is ready.

## Finance changes (consumer side)
1. Subscribe `internal/consumer` to `iag.operations`, filter
   `warehouse.movement.posted`.
2. Book, **idempotent on `movement_id`** (reuse the existing posted-entry
   idempotency on `source_event_id`):
   - `receipt`    → Dr 1400 Inventory / Cr 2150 GR/IR Clearing (total_cost)
   - `issue`      → Dr 5000 COGS       / Cr 1400 Inventory     (total_cost)
   - `adjustment` → Dr/Cr 1400 vs a shrinkage/write-off expense account
3. Ignore zero-cost/transfer movements. Memo = `ref`. Skip cleanly if
   `total_cost` is absent (costing flag off upstream).

## Procurement changes (GR/IR close-out)
On supplier-bill match, book Dr 2150 GR/IR Clearing / Cr 2000 AP so the GR/IR
account nets to zero once goods are both received and invoiced (three-way match).
Requires procurement to reference the GRN so finance can tie bill ↔ receipt.

## Cross-cutting
- **Idempotency:** `movement_id` (finance) and `ref` (procurement) dedupe retries.
- **Ordering:** partition `iag.operations` by `item_id` so per-item cost math
  stays ordered; finance tolerates out-of-order via idempotency + additive posting.
- **Backfill:** on enable, post an opening `1400` balance = current valuation
  (one adjustment journal) so the GL matches on-hand from day one.
- **Rollout:** (1) finance consumer live but warehouse costing flag off (no-op);
  (2) enable warehouse costing in staging, verify GL ties to a valuation report;
  (3) enable procurement GR/IR netting; (4) production with the opening backfill.

## Effort
Warehouse ~2–3 d (schema + WAC + valued emit + tx), finance ~1–2 d (consumer +
tests), procurement ~1 d (GR/IR match). Cross-team; sequence per rollout above.
