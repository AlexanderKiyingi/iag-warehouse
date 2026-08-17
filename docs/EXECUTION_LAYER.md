# Warehouse execution layer

Everything here was added to close the gap between an inventory ledger — which
records decisions people have already made — and a warehouse system, which makes
some of those decisions and tells someone to go and do them.

Migrations 018–024. All of it is additive: an installation that configures none
of it behaves exactly as the service did before.

---

## 1. Unit-of-measure conversion (migration 018)

`wh_items.uom` is the **base unit**. Every balance, movement, cost and event in
this service is in base units and always was.

`wh_item_uoms` declares alternates with a `factor` = base units per alternate
unit (a 60 kg bag of coffee against a `kg` base is factor 60).

Document lines now store both figures: `qty` in base units, and
`entered_qty` / `entered_uom` / `uom_factor` as keyed. **`unit_cost` is rebased
too** — a price of 600,000 per bag is stored as 10,000 per kg, so valuation never
has to know that case sizes exist.

Behaviour is lenient until configured and strict afterwards:

| Item has alternates? | Unknown unit on a line |
|---|---|
| No | accepted at factor 1 (pre-018 behaviour) |
| Yes | rejected — the item's unit regime is declared |

```
GET    /api/v1/items/:id/uoms
POST   /api/v1/items/:id/uoms        {uom, factor, is_purchase_default, is_sales_default}
POST   /api/v1/items/:id/convert     {qty, uom} -> {base_qty, base_uom, factor}
DELETE /api/v1/item-uoms/:id
```

## 2. Directed putaway (migration 019)

A receipt line that names no `bin_code` is now **directed** instead of rejected.

A rule is *match* + *target scope* + *strategy*. Rules run in `priority` order
(lowest first) and the first that yields a legal bin wins. Every criterion is
nullable; nullable means "don't care", so an all-null rule is a valid default.

Strategies: `fixed_bin`, `consolidate`, `empty_bin`, `least_used`,
`capacity_fit`.

Two things worth knowing:

- **Capacity is enforced for every strategy**, not just `capacity_fit`. It needs
  `wh_items.weight_kg`; a weight of 0 (the default) means *unknown*, which is
  treated as unconstrained rather than as weightless.
- **A facility-scoped rule only fires when the request names that facility.**
  Otherwise directing goods would be a guess, and the guess is a bin at another
  site. Send `facility_code` on the receipt.

```
GET    /api/v1/putaway-rules
POST   /api/v1/putaway-rules
PATCH  /api/v1/putaway-rules/:id
DELETE /api/v1/putaway-rules/:id
POST   /api/v1/putaway/resolve       {item_sku, qty, uom, facility_code} -> suggestion
```

`409` with "no putaway bin available" means the warehouse is genuinely full —
it is not a malformed request.

## 3. Replenishment (migration 020)

`wh_stock_thresholds` and `wh_items.min_qty` answer *should we buy more*. A
replenishment level answers *is there stock on site that should be moved to the
pick face*. Both are needed; neither substitutes for the other.

An **open task reserves its source stock**, exactly like an open pick list, so
planned replenishment cannot be picked out from under itself. Completing
consumes the reservation and posts a normal transfer movement; a short move
releases the shortfall rather than leaving it dangling.

A partial unique index on `(item_id, to_bin_id) WHERE status = 'open'` makes the
generator safe to run on a schedule — a second run is a no-op, not a duplicate.

```
GET    /api/v1/replenishment-levels
POST   /api/v1/replenishment-levels  {item_sku, bin_code, min_qty, max_qty, source_zone_code}
DELETE /api/v1/replenishment-levels/:id
GET    /api/v1/replenishment-tasks
POST   /api/v1/replenishment-tasks
POST   /api/v1/replenishment-tasks/generate         (service-callable)
POST   /api/v1/replenishment-tasks/:id/complete     {moved_qty?}
POST   /api/v1/replenishment-tasks/:id/cancel
```

Scheduled job: `cmd/jobs/replenish`.

## 4. Cycle counting as a controlled workflow (migration 021)

Previously a "cycle count" was a stock adjustment with a different label: one
person, one instant write, no record of what the system believed beforehand.

Now: **snapshot → count (blind) → submit → review → approve**. Nothing touches a
balance until approval, and approval posts through the ordinary adjustment path
so the movement ledger, valuation and finance GL all see it normally.

- **Blind by default.** System quantities are withheld from *every* endpoint
  while a blind task is being counted — a leak from one endpoint is not a blind
  count.
- **The snapshot is taken once**, at creation. Re-reading at approval would
  silently absorb everything that moved while the counter walked the aisle.
- **Tolerance decides what needs a human.** Inside → auto-accepted on submit.
  Outside → must be explicitly accepted or sent for recount. Either
  `tolerance_pct` or `tolerance_value` alone is sufficient.
- **Segregation of duties**: the raiser, submitter and anyone who counted a line
  cannot approve. Governed by `WAREHOUSE_COUNT_REQUIRE_SEPARATE_APPROVER`
  (**default on**; set `false` only for single-operator sites).
- Uncounted lines block approval. An uncounted position is not a matching one.

ABC classification ranks items by outbound consumption value over a trailing
window and sets a counting interval per class (A 30d / B 90d / C 180d by
default). With costing disabled it falls back to ranking on quantity and reports
`basis: "consumption_quantity"` rather than silently calling everything C.

```
GET  /api/v1/count-tasks
POST /api/v1/count-tasks             {scope_type: zone|bin|item|abc, scope_ref, blind, tolerance_pct, tolerance_value, due_only}
GET  /api/v1/count-tasks/:id
POST /api/v1/count-tasks/:id/lines                      (found stock the system didn't know about)
POST /api/v1/count-tasks/:id/lines/:lineId/count        {counted_qty, uom?, note}
POST /api/v1/count-tasks/:id/submit
POST /api/v1/count-tasks/:id/lines/:lineId/status       {status: accepted|rejected|recount}
POST /api/v1/count-tasks/:id/reopen
POST /api/v1/count-tasks/:id/approve
POST /api/v1/count-tasks/:id/cancel
POST /api/v1/abc-classification/run  {days, a_pct, b_pct, intervals}
```

## 5. Handling units / licence plates (migration 022)

A pallet, carton, tote or bag with a plate. Scanning one plate identifies
everything on it; moving one plate moves everything on it.

**`wh_hu_contents` is not a second stock ledger.** `wh_stock_balances` stays
authoritative; contents record which of the stock standing in a bin is on which
plate. Moving a plate posts ordinary transfer movements. The cost of that choice
is one invariant the application holds under a row lock: **contents in a bin can
never exceed the bin's balance**.

Loading and unloading move nothing — they are bookkeeping about what is sitting
on what. Only `move` is a stock movement.

Stock reserved for an open pick list **will refuse to move**. The pick has
already sent someone to a specific bin; relocating the goods turns that into a
wild goose chase.

```
GET  /api/v1/handling-units
POST /api/v1/handling-units          {lpn?, hu_type, bin_code}   (lpn auto-assigned if blank)
GET  /api/v1/handling-units/:lpn
POST /api/v1/handling-units/:lpn/load|unload   {item_sku, qty, uom, lot_key}
POST /api/v1/handling-units/:lpn/move          {to_bin_code}
POST /api/v1/handling-units/:lpn/nest          {parent_lpn}      (empty un-nests)
POST /api/v1/handling-units/:lpn/status        {status}
```

## 6. Barcodes, scanning and short picks (migration 023)

`wh_barcodes` resolves a scanned string across every scannable entity. If a code
is not registered it falls back to natural keys — bin code, SKU, LPN, asset tag —
so **scanning works on day one**, before any labels are registered.

An item barcode carries a `uom`; `qty_per_scan` is resolved from the conversion
factor **at registration time** and stored, so redefining a case size later
cannot retroactively change what past scans meant.

**Short picks.** `picked_set` distinguishes "nobody has been to this line" from
"the picker went and found none" — both read zero on `picked_qty` alone. Confirm
uses `qty` when `picked_set` is false (exactly the old all-or-nothing behaviour,
so existing callers are unaffected) and `picked_qty` when true, **releasing the
reservation on the shortfall**. A short pick requires a reason.

```
GET    /api/v1/barcodes
POST   /api/v1/barcodes              {barcode, entity_type, item_sku|bin_code|asset_tag|lpn, uom}
DELETE /api/v1/barcodes/:id
GET    /api/v1/scan/resolve?code=…
POST   /api/v1/scan/move             {item_sku, from_bin_code, to_bin_code, qty, uom}
POST   /api/v1/pick-lists/:id/lines/:lineId/pick   {picked_qty, uom?, short_reason, bin_code?}
```

Sending `bin_code` on a pick has it checked against the line rather than trusted —
a picker at the wrong bin is precisely the mistake scanning exists to catch.

## 7. Gate passes and equipment handover slips (migration 024)

`wh_gate_passes` (migration 010) is a flat note with five text fields, no line
items, no authorisation and nothing for a guard to check. It is untouched because
storesiag reads it. **`wh_slips` is the controlled document that replaces it.**

Two kinds share the table because they share everything that matters:

- `equipment_handover` — custody moving between people or sites
- `cargo_gate_pass` — goods leaving the premises, shown to security at the gate

Lifecycle: **draft → issued → released → returned → closed** (plus `rejected` /
`cancelled`).

- A draft has **no number and no token** — it is deliberately worthless at the
  gate, and prints with a "not valid" banner and no barcode.
- Authorisation assigns the number (`GP-…` / `EH-…` from a real yearly sequence)
  and a random 80-bit `verify_token`.
- **Separate authoriser** is enforced by
  `WAREHOUSE_SLIP_REQUIRE_SEPARATE_AUTHORIZER` (**default on**).
- `verify` is a **read** — checking a slip and letting the lorry out are two
  decisions, and only the second is recorded as having happened.
- A released slip **cannot be cancelled**: the goods are outside the fence and
  erasing that would destroy the only record.
- Returnable slips stay outstanding until every line is back, which is what makes
  them chaseable.

The printed sheet is self-contained HTML — inline CSS, an **inline SVG Code 39
barcode**, no external references — because it has to print from a depot machine
with no internet. Code 39 is generated in-repo (`internal/slips/code39.go`); it
encodes exactly the characters our numbers and tokens use and is read by every
warehouse scanner without configuration. Unencodable input renders **no** symbol
rather than a partial one: a barcode that fails at the barrier is worse than none.

```
GET  /api/v1/slips?slip_type=&status=&overdue=true
POST /api/v1/slips
GET  /api/v1/slips/verify/:token     (security)
GET  /api/v1/slips/:id
GET  /api/v1/slips/:id/print         (?format=json for the raw data)
POST /api/v1/slips/:id/authorize|reject
POST /api/v1/slips/:id/release       (security)
POST /api/v1/slips/:id/return|close|cancel
```

Scheduled job: `cmd/jobs/overdueslips`.

---

## Configuration

| Variable | Default | Effect |
|---|---|---|
| `WAREHOUSE_COUNT_REQUIRE_SEPARATE_APPROVER` | `true` | Count SoD. Only `false` disables it. |
| `WAREHOUSE_SLIP_REQUIRE_SEPARATE_AUTHORIZER` | `true` | Slip SoD. Only `false` disables it. |
| `WAREHOUSE_ORG_NAME` | `Inspire Africa Group` | Printed at the head of slips. |

## Permissions

21 new permissions, registered with auth on boot as usual. The gate-pass ones are
deliberately split three ways — `add_slip` (storeman), `authorize_slip`
(manager), `verify_slip` (security) — because those are three different people
and the control only holds if the system agrees.

## Bugs fixed on the way through

Two pre-existing defects surfaced when these paths were first exercised against a
real database:

1. **`wh_movements.attrs`** — no caller set it, pgx encodes a nil map as SQL
   NULL, and the column is `NOT NULL`. Every receipt post, issue post, transfer
   and adjustment failed on the constraint. Now defaulted to `{}`.
2. **`adjustBalanceTx` and negative deltas** — it used
   `INSERT … ON CONFLICT DO UPDATE`, and Postgres evaluates CHECK constraints
   against the *proposed insert tuple* before resolving the conflict. Once
   migration 005 added `qty >= 0`, every downward adjustment failed even when the
   resulting balance was positive, breaking shrinkage, damage write-offs and
   count corrections. Now locks the position, decides, then writes.

Also fixed: `ListAssets` and `GetAssetByTag` scanned 11 destinations from a
10-column SELECT (`disposed_at` was never added after migration 006).

## Testing

`internal/store/wms_integration_test.go` runs the whole layer against a real
Postgres. It skips unless `WAREHOUSE_TEST_DATABASE_URL` is set:

```bash
createdb wh_wms_test
WAREHOUSE_TEST_DATABASE_URL="postgres://user:pass@localhost:5432/wh_wms_test?sslmode=disable" \
  go test ./internal/store/
```

These are worth the setup: most of what was added is enforced by SQL — partial
unique indexes, CHECK constraints, `FOR UPDATE` ordering, a recursive CTE — and
none of that is exercised by a test that stubs the database out. Both bugs above
were found this way.

## Deliberately not built

Wave/batch/cluster picking, slotting optimisation, and labour management. IAG
runs mills, workshops, yards and depots — not 200-picker fulfilment centres — and
the throughput does not justify them. They are the Manhattan/Blue Yonder value
proposition and would be dead weight here.
