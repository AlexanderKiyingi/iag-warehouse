# iag-warehouse

Warehouse operations microservice — physical stock execution for raw materials, finished goods, consumables, spare parts, and equipment assets.

| Field | Value |
|-------|-------|
| **Port** | `4005` |
| **Audience** | `iag.warehouse` |
| **Gateway prefix** | `/api/v1/warehouse` |
| **Status** | Implemented — [platform integration](docs/PLATFORM_INTEGRATION.md) |
| **Remote** | [iag-warehouse](https://github.com/AlexanderKiyingi/iag-warehouse) |

## Scope

| Domain | Warehouse responsibility |
|--------|--------------------------|
| **Raw materials** | Receive, store, issue to production lines (linked to SCM batches) |
| **Finished products** | Receive from production, QC hold/release, pick/pack for dispatch |
| **Consumables** | Stock and issue to departments (fleet, maintenance, utilities, …) |
| **Spare parts** | SKU stock, equipment compatibility, low-stock signals |
| **Equipment** | Serialized asset check-in/out and custody while in storage |

**Quantity ledger** (canonical on-hand) → **`iag-inventory`** (planned). **Coffee batch/lot identity** → **`iag-supply-chain`**. **PO / GRN documents** → **`iag-procurement`**.

## Documentation

- [**Implementation plan**](docs/IMPLEMENTATION_PLAN.md) — data model, APIs, Kafka contracts, phases
- [**Platform integration**](docs/PLATFORM_INTEGRATION.md) — gateway, env, events
- [**Production checklist**](docs/PRODUCTION_CHECKLIST.md)

## Quick start

```bash
cd services/operations/warehouse
cp config/.env.example .env
go run ./cmd/server
curl http://localhost:4005/ready
curl http://localhost:8080/api/v1/warehouse/ready   # via gateway
```

Registry: [`subrepos.json`](../../../subrepos.json)
