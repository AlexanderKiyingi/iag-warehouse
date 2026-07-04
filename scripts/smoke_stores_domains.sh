#!/usr/bin/env bash
# Smoke test for the migration-010 stores-domain endpoints (thresholds, returns,
# gate passes, warranties, event requests). Exercises list + create + update +
# delete against a running gateway/warehouse stack.
#
# Usage:
#   GATEWAY=http://localhost:8080 TOKEN=<jwt-with-aud-iag.gateway+iag.warehouse> \
#     ./scripts/smoke_stores_domains.sh
#
# The token must carry the relevant warehouse.* permissions (or be superuser).
# Calls go through the gateway, which rewrites /api/v1/warehouse → the service.
set -euo pipefail

GATEWAY="${GATEWAY:-http://localhost:8080}"
BASE="${GATEWAY%/}/api/v1/warehouse/api/v1"
TOKEN="${TOKEN:?set TOKEN to a bearer JWT}"
AUTH=(-H "Authorization: Bearer ${TOKEN}" -H "Content-Type: application/json")

say() { printf '\n=== %s ===\n' "$1"; }
id_of() { sed -n 's/.*"id":"\([0-9a-f-]\{36\}\)".*/\1/p' | head -1; }

smoke() { # name path create-json
  local name="$1" path="$2" body="$3"
  say "$name"
  curl -fsS "${AUTH[@]}" "${BASE}${path}" >/dev/null && echo "list ok"
  local created id
  created=$(curl -fsS "${AUTH[@]}" -X POST "${BASE}${path}" -d "$body")
  id=$(printf '%s' "$created" | id_of)
  echo "created ${id}"
  curl -fsS "${AUTH[@]}" -X PATCH "${BASE}${path}/${id}" -d "$body" >/dev/null && echo "patch ok"
  curl -fsS "${AUTH[@]}" -X DELETE "${BASE}${path}/${id}" >/dev/null && echo "delete ok"
}

smoke "thresholds" "/stock-thresholds" '{"item":"Smoke SKU","dept":"Stores","min_qty":5,"reorder_qty":20,"alert_method":"System","status":"Active"}'
smoke "returns"    "/returns"          '{"item":"Smoke Return","qty":2,"returned_by":"smoke","condition":"Good","action":"Restocked","return_date":"2026-06-29"}'
smoke "warranties" "/warranties"       '{"item":"Smoke Asset","supplier":"ACME","purchase_date":"2026-01-01","expiry_date":"2027-01-01","duration":"1 year","status":"Active"}'
smoke "events"     "/event-requests"   '{"event_name":"Smoke Event","items":"chairs","qty":10,"dept":"Events","requested_by":"smoke","needed_by":"2026-07-01"}'

# Gate passes also expose a /return action.
say "gate-passes"
curl -fsS "${AUTH[@]}" "${BASE}/gate-passes" >/dev/null && echo "list ok"
GP=$(curl -fsS "${AUTH[@]}" -X POST "${BASE}/gate-passes" -d '{"items":"laptop","issued_to":"smoke","dept":"IT","date_out":"2026-06-29","return_by":"2026-07-05"}')
GPID=$(printf '%s' "$GP" | id_of)
echo "created ${GPID}"
curl -fsS "${AUTH[@]}" -X POST "${BASE}/gate-passes/${GPID}/return" -d '{"return_date":"2026-06-30"}' >/dev/null && echo "return ok"
curl -fsS "${AUTH[@]}" -X DELETE "${BASE}/gate-passes/${GPID}" >/dev/null && echo "delete ok"

# New capability read endpoints (list-only GETs the storesiag views hydrate from).
say "capability reads"
for p in /adjustments /cycle-counts /transfers /pack-sessions /movements /spare-compat; do
  curl -fsS "${AUTH[@]}" "${BASE}${p}" >/dev/null && echo "GET ${p} ok"
done

say "ALL STORES-DOMAIN SMOKE CHECKS PASSED"
