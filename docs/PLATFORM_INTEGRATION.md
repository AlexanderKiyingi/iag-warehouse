# IAG Warehouse — platform integration

Physical stock execution behind the **API gateway**, with **iag-authentication** IAM and Kafka on **`iag.operations`**.

| Field | Value |
|-------|-------|
| **Port** | `4005` |
| **Audience** | `iag.warehouse` |
| **Gateway prefix** | `/api/v1/warehouse` |
| **Postgres schema** | `warehouse` |
| **Role** | `svc_iag_warehouse` |

## Gateway routes

| Gateway path | Upstream |
|--------------|----------|
| `/api/v1/warehouse/health` | `:4005/health` |
| `/api/v1/warehouse/ready` | `:4005/ready` |
| `/api/v1/warehouse/api/v1/*` | `:4005/api/v1/*` |

Compose: `UPSTREAM_WAREHOUSE=http://warehouse:4005` on `api-gateway`.

## Environment

| Variable | Purpose |
|----------|---------|
| `DATABASE_URL` | `postgres://svc_iag_warehouse:…@pgbouncer:6432/iag_platform` |
| `AUDIENCE` | `iag.warehouse` |
| `JWKS_URL` / `JWT_ISSUER` | Bearer JWT verification |
| `SERVICE_CLIENT_ID` / `SERVICE_CLIENT_SECRET` | Registers `warehouse.*` permissions at boot |
| `EVENT_BUS_ENABLED` | `true` + `KAFKA_BROKERS` for outbox → `iag.operations` |
| `KAFKA_COMMERCIAL_TOPIC` | Consumes `procurement.grn.posted` |
| `KAFKA_PRODUCTION_TOPIC` | Consumes `mes.*.completed` |
| `KAFKA_QUALITY_TOPIC` | Consumes `qc.coa.issued` |
| `KAFKA_OPERATIONS_TOPIC` | Consumes `dms.dispatch.created` |

## Kafka — published (`iag.operations`)

| Event type | When |
|------------|------|
| `warehouse.receipt.posted` | Receipt posted |
| `warehouse.issue.posted` | Issue posted |
| `warehouse.transfer.completed` | Transfer posted |
| `warehouse.production.consumed` | RM backflush |
| `warehouse.production.output` | FG receipt |
| `warehouse.pick.confirmed` | Pick list confirmed |
| `warehouse.asset.checked_out` | Equipment custody transfer |
| `warehouse.asset.disposed` | Asset disposal executed (carries `asset_tag`, `method`, `proceeds`, `currency`, optional `book_value`) — iag-finance books the gain/loss |
| `warehouse.stock.below_minimum` | Low-stock job |
| `warehouse.movement.posted` | Inventory handoff (Phase 4 bridge) |

## Kafka — consumed

| Topic | Event type | Action |
|-------|------------|--------|
| `iag.commercial` | `procurement.grn.posted` | Draft warehouse receipt |
| `iag.production` | `mes.drymill.completed` | Optional FG output |
| `iag.quality` | `qc.coa.issued` | Release QC hold |
| `iag.operations` | `dms.dispatch.created` | Dispatch coordination |

## Local dev

```bash
cd services/operations/warehouse
cp config/.env.example .env
go run ./cmd/server
curl http://localhost:4005/ready
curl http://localhost:8080/api/v1/warehouse/ready   # via gateway
```

Full stack: `pnpm infra:up` from repo root (includes `warehouse` service on `:4005`).

## Related services

| Service | Integration |
|---------|-------------|
| **iag-procurement** | GRN posted → draft receipt |
| **iag-mes** | Production stages → FG output (optional consumer) |
| **iag-quality-control** | CoA → release FG hold |
| **iag-dms** | Pick confirm ↔ dispatch |
| **iag-finance** | `warehouse.asset.disposed` → gain/loss on disposal + fixed-asset de-recognition |
| **iag-inventory** | Future qty ledger via `warehouse.movement.posted` |
