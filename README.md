# iag-warehouse

Warehouse operations microservice — inbound/outbound, bin locations, and pick/pack for the IAG platform.

| Field | Value |
|-------|-------|
| **Port** | `4005` |
| **Status** | Scaffold |
| **Remote** | [iag-warehouse](https://github.com/AlexanderKiyingi/iag-warehouse) |

## Planned role

Physical warehouse execution complementing **`iag-inventory`** (stock levels) and **`iag-supply-chain`** (coffee batches/lots). Gateway auth, Kafka events on `iag.operations`, integration with DMS dispatch where applicable.

## Quick start

```bash
cd services/operations/warehouse
# implementation pending
```

Registry: [`subrepos.json`](../../../subrepos.json)
