# iag-warehouse — implementation plan

| Field | Value |
|-------|-------|
| **Status** | Implemented |
| **Date** | 2026-06-09 |
| **Port** | `4005` |
| **Audience** | `iag.warehouse` |
| **Gateway prefix** | `/api/v1/warehouse` |
| **Kafka topic** | `iag.operations` (primary), consumes `iag.production`, `iag.commercial`, `iag.supply-chain` |
| **Related** | [TRACEABILITY_AND_SUPPLIER_PLATFORM.md](../../../../docs/planning/TRACEABILITY_AND_SUPPLIER_PLATFORM.md), sibling scaffolds `iag-inventory`, `iag-infrastructure-management` |

---

## 1. Executive summary

`iag-warehouse` is the **physical execution layer** for all stock held on-site: receiving, put-away, bin location, picking, issuing, transferring, and cycle counting. It must cover five material domains in one service:

| Domain | Examples | Typical source | Typical destination |
|--------|----------|----------------|---------------------|
| **Raw materials** | Green coffee, packaging film, chemicals | Procurement PO / farmer intake | Production lines (MES) |
| **Finished products** | Roasted coffee, packaged SKUs, instant | Production output (MES / dry mill) | DMS dispatch, export staging |
| **Consumables** | Diesel, lubricants, PPE, office supplies | Procurement / internal transfer | Fleet, maintenance, utilities, departments |
| **Spare parts** | Filters, belts, brake pads | Procurement | Maintenance work orders, fleet workshop |
| **Equipment (assets)** | Roasters, vehicles in storage, tools | Capex receipt / transfer | Production floor, fleet yard, project sites |

**Warehouse owns physical state** (where something is, what movement happened). **`iag-inventory`** (when live) owns **quantity ledger** and SKU master for fungible stock. **`iag-supply-chain`** retains coffee **batch/lot identity** for traceability. This plan assumes warehouse and inventory are built in parallel with explicit event contracts so warehouse can ship first with a local balance table, then delegate ledger authority to inventory in Phase 4.

---

## 2. Goals and non-goals

### Goals

| ID | Goal |
|----|------|
| G1 | Single warehouse service for all five domains with a shared movement ledger |
| G2 | Bin-level location tracking (facility → zone → bin) |
| G3 | Inbound from procurement; outbound to production and departments |
| G4 | Production consumption (RM issue) and output (FG receipt) tied to MES / SCM batch ids |
| G5 | Serialized equipment check-in/out with custody history |
| G6 | Spare parts stock with reorder signals and optional equipment linkage |
| G7 | Kafka events on `iag.operations` for DMS, traceability, inventory, finance hooks |
| G8 | Platform JWT (`aud=iag.warehouse`), RBAC, gateway ingress, transactional outbox |

### Non-goals (v1)

- Full WMS optimization (wave planning, slotting algorithms, voice picking)
- GS1 / EPCIS compliance
- Owning coffee batch master data (SCM) or SKU catalog (inventory / procurement items table long-term)
- Fleet telematics or vehicle registry (fleet service)
- Maintenance work-order system (future infrastructure-management); v1 accepts manual issue requests
- Multi-tenant 3PL warehousing

---

## 3. Service boundaries

```text
                    ┌─────────────────────┐
                    │   iag-api-gateway   │
                    │  :8080              │
                    └──────────┬──────────┘
                               │
         ┌─────────────────────┼─────────────────────┐
         │                     │                     │
         ▼                     ▼                     ▼
┌─────────────────┐   ┌─────────────────┐   ┌─────────────────┐
│ iag-procurement │   │ iag-warehouse   │   │ iag-mes         │
│ PO, GRN header  │──►│ receipts, bins, │◄──│ production      │
│ commercial docs │   │ issues, assets  │   │ stage events    │
└─────────────────┘   └────────┬────────┘   └─────────────────┘
                               │
              ┌────────────────┼────────────────┐
              │                │                │
              ▼                ▼                ▼
     ┌──────────────┐  ┌──────────────┐  ┌──────────────┐
     │ iag-inventory│  │ iag-dms      │  │ iag-traceability│
     │ qty ledger   │  │ FG dispatch  │  │ custody/story │
     └──────────────┘  └──────────────┘  └──────────────┘
              ▲
              │ party_id / batch_business_id from SCM
     ┌──────────────┐
     │ iag-supply-  │
     │ chain        │
     └──────────────┘
```

| Concern | System of record | Warehouse role |
|---------|------------------|----------------|
| PO / vendor / GRN document | Procurement | Execute physical receipt; confirm qty & bin |
| SKU / reorder point (target) | Inventory (procurement `items` interim) | Mirror SKU ref on `wh_items`; emit adjustments |
| Coffee batch / lot id | Supply chain | Attach `batch_business_id` / `lot_id` on RM & FG moves |
| On-hand by outlet | DMS | Consume `warehouse.pick.confirmed` for dispatch staging |
| Vehicle / equipment registry | Fleet / infra-mgmt (future) | Store **physical custody** when asset is in a warehouse bin |
| Mill stage completion | MES | Trigger RM consumption templates & FG output receipts |

---

## 4. Material taxonomy

All stock is stored as a **`wh_item`** with a **`material_class`** and **`tracking_mode`**:

| `material_class` | `tracking_mode` | Key fields | Notes |
|------------------|-----------------|------------|-------|
| `raw_material` | `bulk`, `lot` | `sku`, `batch_business_id?`, `uom` | Coffee cherry/green links to SCM batch |
| `finished_good` | `lot`, `sku` | `sku`, `lot_id?`, `qc_hold?` | QC gate may block pick (consume `qc.coa.issued`) |
| `consumable` | `bulk` | `sku`, `cost_center?`, `department?` | Issued to `fleet`, `maintenance`, `utilities`, etc. |
| `spare_part` | `sku`, `serial` (optional) | `sku`, `compatible_asset_types[]` | Min/max per bin or facility |
| `equipment` | `serial` | `asset_tag`, `serial_no`, `condition` | Not consumed on issue — custody transfer |

**Department dimension** (for consumables & spares): `fleet`, `maintenance`, `utilities`, `production`, `quality`, `admin`, `dms`, `other`. Used on issue headers for chargeback / analytics.

---

## 5. Core data model (Postgres schema `warehouse`)

### 5.1 Location hierarchy

```
wh_facilities (code, name, site_type: mill|fg_warehouse|workshop|yard)
  └── wh_zones (code, zone_type: receiving|bulk|cold|quarantine|staging|assets)
        └── wh_bins (code, capacity_kg?, temperature_band?, status: active|blocked)
```

### 5.2 Items and balances

| Table | Purpose |
|-------|---------|
| `wh_items` | Material master row: class, tracking_mode, sku (external ref), name, uom, attrs JSON |
| `wh_stock_balances` | `(item_id, bin_id, lot_key, serial_key)` → `qty`, `status` (available, hold, damaged) |
| `wh_lots` | Optional lot registry: `lot_key`, `batch_business_id`, `expiry_on`, `origin` |
| `wh_assets` | Equipment: `asset_tag`, serial, item_id, current_bin_id, condition, book_value_ref |

`lot_key` / `serial_key` are nullable sentinels for bulk tracking.

### 5.3 Movement ledger (append-only)

| Table | Purpose |
|-------|---------|
| `wh_movements` | Every stock change: type, item, from_bin, to_bin, qty, refs, actor, occurred_at |
| `wh_receipts` / `wh_receipt_lines` | Inbound header/lines (procurement, production output, returns) |
| `wh_issues` / `wh_issue_lines` | Outbound header/lines (production, department, dispatch) |
| `wh_transfers` / `wh_transfer_lines` | Bin-to-bin / facility-to-facility |
| `wh_adjustments` | Cycle count & shrinkage (permission-gated) |

Movement types: `receipt`, `issue`, `transfer`, `production_consume`, `production_output`, `return`, `adjustment`, `asset_checkin`, `asset_checkout`.

### 5.4 Integration refs

| Table | Purpose |
|-------|---------|
| `wh_external_refs` | `(source_service, source_type, source_id)` → movement/receipt/issue id |
| `wh_event_outbox` | Transactional Kafka outbox (same pattern as fleet/DMS) |

### 5.5 Idempotency

All write APIs accept `Idempotency-Key` header (or body `idempotency_key`). Unique index on `(actor_id, idempotency_key)` or `(source_service, source_id)` for consumer handlers.

---

## 6. API surface (v1)

Base path: `/api/v1/warehouse` (service) → gateway `/api/v1/warehouse/api/v1/*`.

### 6.1 Platform

| Method | Path | Permission |
|--------|------|------------|
| GET | `/health`, `/ready` | public |
| GET | `/api/v1/platform/status` | staff |
| GET | `/api/v1/bootstrap` | `warehouse.view_overview` |

### 6.2 Locations

| Method | Path | Permission |
|--------|------|------------|
| GET/POST | `/api/v1/facilities` | `warehouse.view_location` / `warehouse.add_location` |
| GET/PATCH | `/api/v1/facilities/:code` | view / `warehouse.change_location` |
| GET/POST | `/api/v1/facilities/:code/zones` | view / add |
| GET/POST | `/api/v1/zones/:code/bins` | view / add |
| GET | `/api/v1/bins/:code/stock` | `warehouse.view_stock` |

### 6.3 Items

| Method | Path | Permission |
|--------|------|------------|
| GET/POST | `/api/v1/items` | `warehouse.view_item` / `warehouse.add_item` |
| GET/PATCH | `/api/v1/items/:id` | view / `warehouse.change_item` |
| GET | `/api/v1/items/:id/balances` | `warehouse.view_stock` |

### 6.4 Inbound

| Method | Path | Permission |
|--------|------|------------|
| POST | `/api/v1/receipts` | `warehouse.add_receipt` |
| POST | `/api/v1/receipts/:id/post` | `warehouse.post_receipt` |
| GET | `/api/v1/receipts` | `warehouse.view_receipt` |
| POST | `/api/v1/receipts/from-grn` | `warehouse.add_receipt` — links `procurement` GRN id |

Receipt line fields: `item_id`, `qty`, `uom`, `bin_code`, `lot_key?`, `batch_business_id?`, `po_id?`, `grn_id?`.

### 6.5 Outbound

| Method | Path | Permission |
|--------|------|------------|
| POST | `/api/v1/issues` | `warehouse.add_issue` |
| POST | `/api/v1/issues/:id/post` | `warehouse.post_issue` |
| GET | `/api/v1/issues` | `warehouse.view_issue` |
| POST | `/api/v1/issues/for-department` | `warehouse.issue_consumable` |

Issue fields: `department`, `cost_center?`, `production_order_ref?`, `batch_business_id?`, lines with `bin_code`, `qty`.

### 6.6 Production bridge

| Method | Path | Permission |
|--------|------|------------|
| POST | `/api/v1/production/consume` | `warehouse.production_consume` |
| POST | `/api/v1/production/output` | `warehouse.production_output` |

Called by MES completion handlers or Kafka consumer. Consumption deducts RM from configured bins; output creates FG into staging bins.

### 6.7 Transfers and adjustments

| Method | Path | Permission |
|--------|------|------------|
| POST | `/api/v1/transfers` | `warehouse.add_transfer` |
| POST | `/api/v1/adjustments` | `warehouse.adjust_stock` |
| POST | `/api/v1/cycle-counts` | `warehouse.cycle_count` |

### 6.8 Assets and spare parts

| Method | Path | Permission |
|--------|------|------------|
| GET/POST | `/api/v1/assets` | `warehouse.view_asset` / `warehouse.add_asset` |
| POST | `/api/v1/assets/:tag/check-in` | `warehouse.checkin_asset` |
| POST | `/api/v1/assets/:tag/check-out` | `warehouse.checkout_asset` |
| GET | `/api/v1/spare-parts/low-stock` | `warehouse.view_stock` |
| GET | `/api/v1/spare-parts/by-asset/:asset_tag` | `warehouse.view_item` |

### 6.9 Pick / pack (FG → dispatch)

| Method | Path | Permission |
|--------|------|------------|
| POST | `/api/v1/pick-lists` | `warehouse.add_pick` |
| POST | `/api/v1/pick-lists/:id/confirm` | `warehouse.confirm_pick` |
| POST | `/api/v1/pack-sessions` | `warehouse.add_pack` |

`confirm_pick` emits `warehouse.pick.confirmed` for DMS / fleet dispatch staging.

---

## 7. Kafka contracts

### 7.1 Published (`iag.operations`)

| Event type | When | Key fields |
|------------|------|------------|
| `warehouse.receipt.posted` | Receipt posted | `receipt_id`, `lines[]`, `grn_id?`, `po_id?` |
| `warehouse.issue.posted` | Issue posted | `issue_id`, `department`, `lines[]` |
| `warehouse.transfer.completed` | Transfer posted | `transfer_id`, `from_facility`, `to_facility` |
| `warehouse.production.consumed` | RM backflush | `batch_business_id`, `facility`, `lines[]` |
| `warehouse.production.output` | FG receipt | `batch_business_id`, `sku`, `qty`, `bin_code` |
| `warehouse.pick.confirmed` | Pick confirmed | `pick_list_id`, `order_ref?`, `lines[]` |
| `warehouse.asset.checked_out` | Equipment leaves bin | `asset_tag`, `to_department`, `custodian` |
| `warehouse.stock.below_minimum` | Spare/consumable reorder | `item_id`, `sku`, `qty`, `min_qty` |

### 7.2 Consumed

| Topic | Event type | Action |
|-------|------------|--------|
| `iag.commercial` | `procurement.grn.posted` (new — add to procurement) | Create draft receipt |
| `iag.production` | `mes.wetmill.completed`, `mes.drying.completed`, `mes.drymill.completed` | Optional auto-output / stage-based RM rules |
| `iag.quality` | `qc.coa.issued` | Release QC hold on FG lot |
| `iag.supply-chain` | `scm.batch.received` (future) | Pre-create lot + receiving bin expectation |
| `iag.operations` | `dms.dispatch.created` | Reserve / confirm picked stock |

### 7.3 Inventory handoff (Phase 4)

When `iag-inventory` is live, warehouse stops being quantity authority for fungible stock:

- On each posted movement → `warehouse.movement.posted` → inventory applies delta
- Inventory replies with `inventory.balance.updated` (optional read-model sync)

Until then, warehouse `wh_stock_balances` is authoritative for operations UI.

---

## 8. RBAC

Register at boot with `iag-authentication` (pattern: traceability, fleet).

### Permission catalogue (initial)

| Codename | Description |
|----------|-------------|
| `warehouse.view_overview` | Dashboard / bootstrap |
| `warehouse.view_location` | Facilities, zones, bins |
| `warehouse.add_location` / `warehouse.change_location` | Master data writes |
| `warehouse.view_item` / `warehouse.add_item` / `warehouse.change_item` | Item master |
| `warehouse.view_stock` | Balances, low stock |
| `warehouse.view_receipt` / `warehouse.add_receipt` / `warehouse.post_receipt` | Inbound |
| `warehouse.view_issue` / `warehouse.add_issue` / `warehouse.post_issue` | Outbound |
| `warehouse.issue_consumable` | Department consumable issue |
| `warehouse.production_consume` / `warehouse.production_output` | Production bridge |
| `warehouse.add_transfer` | Bin transfers |
| `warehouse.adjust_stock` / `warehouse.cycle_count` | Adjustments |
| `warehouse.view_asset` / `warehouse.add_asset` | Equipment registry |
| `warehouse.checkin_asset` / `warehouse.checkout_asset` | Asset custody |
| `warehouse.add_pick` / `warehouse.confirm_pick` / `warehouse.add_pack` | Fulfillment |
| `warehouse.admin.read` | Staff audit / monitoring |

### Suggested groups

| Group | Permissions |
|-------|-------------|
| `warehouse-operator` | view_*, add_receipt, add_issue, issue_consumable, add_transfer, add_pick |
| `warehouse-supervisor` | operator + post_*, production_*, confirm_pick, cycle_count |
| `warehouse-admin` | supervisor + adjust_stock, add/change_location, add/change_item, admin.read |
| `production-clerk` | production_consume, production_output, view_stock (production bins) |
| `maintenance-store` | issue_consumable, view/add spare parts, checkout_asset (workshop) |

---

## 9. Phased delivery

### Phase 0 — Scaffold (1–2 weeks)

Deliverables:

- [ ] Go module `iag-warehouse/backend`, Gin router, `cmd/server`
- [ ] Config package (`PORT`, `DATABASE_URL`, `JWKS_URL`, `AUDIENCE=iag.warehouse`)
- [ ] Health / ready probes
- [ ] Postgres migrations: facilities, zones, bins, items, balances, movements, outbox
- [ ] Platform auth middleware (JWKS, fail-closed RBAC)
- [ ] Permission registration at startup
- [ ] Gateway route `/api/v1/warehouse` + `UPSTREAM_WAREHOUSE` in compose
- [ ] `docs/PLATFORM_INTEGRATION.md`, OpenAPI stub

**Reference implementations:** `services/operations/traceability`, `services/operations/fleet`.

### Phase 1 — Locations + item master + balances (2 weeks)

- [ ] CRUD facilities / zones / bins
- [ ] Item master with `material_class` + `tracking_mode`
- [ ] Manual receipt post → balance update + movement row
- [ ] Manual issue → department consumable
- [ ] Stock inquiry by bin / item / facility
- [ ] Seed data: Mbale mill, KLA FG warehouse, workshop store

### Phase 2 — Production & procurement integration (2–3 weeks)

- [ ] `POST /receipts/from-grn` + consumer for `procurement.grn.posted` (requires procurement event)
- [ ] `POST /production/consume` and `/production/output`
- [ ] MES consumer: optional FG receipt on `mes.drymill.completed`
- [ ] SCM `batch_business_id` on coffee RM/FG lines
- [ ] QC hold flag on FG balances; release on `qc.coa.issued`
- [ ] Outbox publisher for all posted movements

### Phase 3 — Assets, spare parts, pick/pack (2–3 weeks)

- [ ] Serialized `wh_assets` check-in/out
- [ ] Spare part ↔ equipment compatibility map
- [ ] Low-stock scanner job → `warehouse.stock.below_minimum`
- [ ] Pick lists + confirm → `warehouse.pick.confirmed`
- [ ] DMS listener for dispatch coordination

### Phase 4 — Inventory delegation & hardening (2 weeks)

- [ ] Integrate `iag-inventory` as quantity authority
- [ ] Cycle count workflow
- [ ] Production invariants: `ENVIRONMENT=production` checklist
- [ ] Audit log + staff monitoring endpoints
- [ ] Load tests on movement posting

---

## 10. Domain workflows

### 10.1 Raw materials → production

```text
Procurement PO approved
  → GRN posted (procurement)
  → warehouse draft receipt (auto or clerk)
  → put-away to RM bulk bin (batch/lot from SCM)
  → MES stage started
  → warehouse.production.consumed (backflush RM per recipe template)
  → traceability custody event (optional consumer)
```

### 10.2 Finished products → dispatch

```text
MES dry mill completed
  → warehouse.production.output → FG staging bin (QC hold)
  → qc.coa.issued → status available
  → DMS order → pick list → confirm pick
  → warehouse.pick.confirmed → dms.dispatch.created
  → fleet load (future)
```

### 10.3 Consumables → departments

```text
Procurement PO (diesel, filters) OR internal stock
  → receipt into consumables bin
  → clerk issues to department=fleet with vehicle_ref optional
  → warehouse.issue.posted
  → finance cost center hook (future)
```

### 10.4 Spare parts → maintenance

```text
Spare SKU received into workshop bin
  → maintenance clerk issues against equipment asset_tag
  → qty decremented; optional serial capture
  → low stock → procurement signal
```

### 10.5 Equipment custody

```text
Capex / transfer receipt
  → asset check-in to equipment zone bin
  → checkout to department=production or fleet
  → warehouse.asset.checked_out
  → infra-mgmt / fleet updates authoritative registry (future)
```

---

## 11. Platform wiring checklist

| Component | Action |
|-----------|--------|
| `shared/services/api-gateway/src/routes.ts` | Add `/api/v1/warehouse` → `:4005` |
| `shared/services/api-gateway/src/policies.ts` | Route policies for `warehouse.*` permissions |
| `deploy/docker-compose.yml` | `warehouse` service + env |
| `subrepos.json` | Update note from scaffold → `language: go`, `framework: gin` |
| `shared/services/authentication` | Seed groups in RBAC docs / migration |
| Postgres | Role `svc_iag_warehouse`, schema `warehouse` |
| `docs/planning/TRACEABILITY_EVENTS.md` | Add warehouse custody event types |

---

## 12. Open decisions

| # | Question | Recommendation |
|---|----------|----------------|
| OQ1 | Does warehouse own SKU master in v1? | **No** — reference procurement `items.sku` / SCM inventory SKU; defer to `iag-inventory` |
| OQ2 | Single DB schema or `warehouse` schema namespace? | **`warehouse` schema** (matches traceability pattern) |
| OQ3 | Auto-backflush RM on MES events? | **Configurable rules** per facility+stage; default manual post in v1 |
| OQ4 | Coffee cherry intake from farmers? | **Receipt with `batch_business_id`** from SCM; warehouse does not create batches |
| OQ5 | Equipment book value? | **Custody only in v1**; finance asset register stays in finance / infra-mgmt |
| OQ6 | GRN posting event from procurement? | **Add `procurement.grn.posted`** in procurement Phase 2.1 (prerequisite) |

---

## 13. Success criteria

- [ ] Clerk can receive green coffee against a GRN into a bin with batch linkage
- [ ] Production clerk can post RM consumption and FG output for a `batch_business_id`
- [ ] Fleet storekeeper can issue diesel consumables to `department=fleet`
- [ ] Maintenance can issue spare parts against an equipment `asset_tag`
- [ ] Equipment can be checked in/out of a warehouse bin with audit trail
- [ ] Pick confirm emits event consumed by DMS dispatch flow
- [ ] All writes idempotent; outbox guarantees at-least-once Kafka delivery
- [ ] Gateway + JWT + RBAC enforced in production mode

---

## 14. Next steps

1. Review and sign off material taxonomy + phase scope.
2. Execute **Phase 0 scaffold** (Go service, gateway, migrations, permissions).
3. Add **`procurement.grn.posted`** event to procurement (small prerequisite PR).
4. Build Phase 1 APIs with seed fixtures for mill + FG + workshop facilities.
