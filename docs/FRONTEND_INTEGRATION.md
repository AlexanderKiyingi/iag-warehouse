# Warehouse Frontend Integration Guide

Connect a SPA or BFF to **iag-warehouse** (`aud=iag.warehouse`, port **4005**).

## Authentication

Every route except `/health`, `/healthz`, `/ready` requires:

```
Authorization: Bearer <jwt>
```

Obtain tokens from `POST /api/v1/authentication/oauth/token`. The JWT must include `iag.warehouse` in `aud` and warehouse permission codenames (`warehouse.*`).

## Base URLs

| Environment | API base |
|---|---|
| Local direct | `http://localhost:4005/api/v1` |
| Via gateway | `http://localhost:8080/api/v1/warehouse/api/v1` |

## Core flows

### Bootstrap

`GET /bootstrap` — facility list, recent receipts/issues, low-stock summary. Requires `warehouse.view_overview`.

### Receipts (inbound)

1. `POST /receipts` — draft with lines (`item_id`, `qty`, `bin_code`, `lot_key`).
2. `POST /receipts/:id/post` — posts to stock (idempotent with `Idempotency-Key` header).
3. `GET /receipts/:id` — detail with lines.

GRN automation: procurement emits `procurement.grn.posted`; warehouse creates a linked draft. Operators complete bin assignment and post.

### Issues (outbound)

1. `POST /issues` or `POST /issues/for-department` (auto-post).
2. `GET /issues/:id`
3. Stock on **QC hold** returns `422` with `"stock on QC hold or damaged"`.

### Pick / dispatch

1. `POST /pick-lists` with `order_ref` (DMS order id) and lines — **reserves** stock (`reserved += qty`); the line's free balance must cover it.
2. `POST /pick-lists/:id/confirm` — consumes the reservation and deducts stock, emits `warehouse.pick.confirmed`.
3. `POST /pick-lists/:id/cancel` — releases the reservation (open lists only; `409` once confirmed).
4. DMS advances matching orders from `picking` → `delivery`.

Balances expose `qty`, `reserved`, and `available` (= `qty − reserved`); issues/transfers can only take `available`.

### Asset disposal

1. `POST /assets/:tag/dispose` — `{method, reason, proceeds, currency, book_value?, gate_pass_no?, authorized_by?}`. Executes immediately, or creates a `pending_approval` request when `WAREHOUSE_REQUIRE_DISPOSAL_APPROVAL=true`. Emits `warehouse.asset.disposed` on execution (iag-finance books the gain/loss).
2. `GET /asset-disposals/approval-tiers` — the amount-band matrix.
3. `POST /asset-disposals/:id/approve` / `/reject` — tiered, distinct approvers; the disposal executes on the final tier's signature (requester may not approve their own).

### Read models

| Endpoint | Permission |
|---|---|
| `GET /movements?movement_type=&item_id=` | `warehouse.view_stock` |
| `GET /pick-lists?status=` | `warehouse.view_stock` |
| `GET /items/:id/balances` | `warehouse.view_stock` |
| `PATCH /zones/:code`, `PATCH /bins/:code` | `warehouse.change_location` |
| `GET/POST/DELETE /spare-compat` | `warehouse.view_item` / `change_item` |

## Permissions

Register via auth seed (`warehouse-operator` group) or runtime `POST /v1/permissions/register` from `iag-warehouse`.

Typical operator group: view/post receipts, issues, picks, transfers, asset check-in/out.

## Events (read-only for FE)

Warehouse publishes on `iag.operations`: `warehouse.receipt.posted`, `warehouse.issue.posted`, `warehouse.pick.confirmed`, `warehouse.stock.below_minimum`, etc. See `packages/events` `warehouseEventTypes`.
