# Warehouse — production checklist

## Required env

- [ ] `ENVIRONMENT=production`
- [ ] `AUTO_MIGRATE=false` (run migrations in CI/CD)
- [ ] `DATABASE_URL` with `svc_iag_warehouse` on `iag_platform`
- [ ] `AUDIENCE=iag.warehouse`
- [ ] `SERVICE_CLIENT_SECRET` ≥ 16 chars
- [ ] Non-wildcard `ALLOWED_ORIGINS` / CORS allowlist
- [ ] `EVENT_BUS_ENABLED=true` and `KAFKA_BROKERS` set
- [ ] `JWKS_URL` points at production authentication

## Gateway

- [ ] `UPSTREAM_WAREHOUSE` configured on api-gateway
- [ ] Users hold `platform.access_warehouse` + domain permissions

## Smoke tests

- [ ] `GET /api/v1/warehouse/ready` → 200
- [ ] `GET /api/v1/warehouse/api/v1/bootstrap` with token → facilities list
- [ ] Post receipt → balance updated → `warehouse.receipt.posted` on outbox
- [ ] Procurement GRN Posted → draft receipt created (Kafka)
- [ ] Pick confirm → `warehouse.pick.confirmed` emitted
- [ ] Low-stock job: `go run ./cmd/jobs/lowstock`

## Operations

- [ ] Outbox drain healthy (no growing `wh_event_outbox` backlog)
- [ ] Admin audit: `GET /api/v1/warehouse/api/v1/admin/monitoring/summary`
