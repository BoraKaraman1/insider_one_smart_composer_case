# Segment rule language, v1

A segment rule says who receives a campaign. Claude proposes one from the marketer's brief, a human may edit it, and the backend validates it and compiles it to parameterized SQL. The rule is data: it never contains SQL, and the model cannot reach anything outside the catalog below.

- Contract: `schemas/segment_rule.schema.json` (JSON Schema 2020-12).
- Go source of truth for the catalog: `internal/segment/catalog.go`. `catalog_test.go` fails if the schema and the catalog drift apart.

## Shape

```json
{
  "version": 1,
  "match": "all",
  "conditions": [
    { "kind": "enum_attribute",   "field": "city", "op": "eq", "value": "istanbul" },
    { "kind": "number_attribute", "field": "days_since_signup", "op": "lte", "value": 90 },
    { "kind": "event", "event": "cart_add", "within_days": 30, "negate": false }
  ]
}
```

| Key | Meaning |
|---|---|
| `version` | always `1` |
| `match` | `all`: every condition must hold. `any`: at least one must hold |
| `conditions` | 1 to 10 conditions. No nesting: structured outputs reject recursive schemas, so a rule is one flat list |

Every object is closed (`additionalProperties: false`), so an unknown key is an error, never ignored.

## Catalog

| Kind | Field / event | Operators | Value |
|---|---|---|---|
| `enum_attribute` | `city` | `eq`, `neq` | one of the city slugs |
| | | `in`, `not_in` | 1 to 20 unique city slugs |
| `enum_attribute` | `language` | `eq`, `neq` / `in`, `not_in` | `tr`, `en` (one, or a list) |
| `enum_attribute` | `loyalty_tier` | `eq`, `neq` / `in`, `not_in` | `bronze`, `silver`, `gold` (one, or a list) |
| `number_attribute` | `days_since_signup` | `eq`, `neq`, `gt`, `gte`, `lt`, `lte` | integer 0 to 3650 |
| `event` | `cart_add`, `purchase`, `page_view` | (none) | `within_days` integer 1 to 365, `negate` boolean |

City slugs: `istanbul`, `ankara`, `izmir`, `bursa`, `antalya`, `adana`, `konya`, `gaziantep`, `kocaeli`, `eskisehir`. They are ASCII on purpose: Turkish casing maps `İ` and `I` differently from English, and ASCII slugs keep every comparison byte-exact.

Not in the catalog, by design: consent, email, phone, names, ids and anything security-critical. A rule cannot select on them or remove a filter on them.

**Operator and value shape are bound together.** The schema has one branch per field and operator group: `city_single` takes `eq`/`neq` with one value, and `city_list` takes `in`/`not_in` with a list. So structured outputs cannot produce `in` with a single string, or `gt` on a city.

## Semantics

The SQL compiler and the Go test oracle must agree on every rule. These definitions are exact, and each has a boundary test.

- **`asOf`** is the evaluation time, passed in by the caller (the API uses now; tests use a fixed time). It is truncated to microseconds, the precision Postgres stores, before any comparison. All durations are exact multiples of 24 h in UTC, so DST never applies.
- **NULL**: every catalog column is NOT NULL. `neq` and `not_in` are plain negations and never hit SQL three-valued logic.
- **`eq` / `neq` / `in` / `not_in`** compare the stored slug exactly. `not_in` is true when the value is in none of the listed slugs.
- **Event condition**: with `negate: false`, the user matches when they have at least one event of that type with `asOf − within_days × 24h ≤ occurred_at ≤ asOf`. The window is inclusive at both ends, and events after `asOf` are ignored. With `negate: true`, the user matches when they have no such event (NOT EXISTS).
- **`days_since_signup`** is `floor((asOf − signup_at) / 24h)`. It compiles to timestamp ranges computed in Go and bound as parameters, with no float math in SQL. With `d(N) = asOf − N × 24h`:

  | Condition | Holds when |
  |---|---|
  | `eq N` | `d(N+1) < signup_at ≤ d(N)` |
  | `neq N` | not `eq N` |
  | `gte N` | `signup_at ≤ d(N)` |
  | `gt N` | `gte N+1`, that is `signup_at ≤ d(N+1)` |
  | `lte N` | `signup_at > d(N+1)` |
  | `lt N` | `lte N−1`, that is `signup_at > d(N)`; `lt 0` matches nobody |

- **`match: any`** ORs the conditions; **`match: all`** ANDs them.
- **Consent is not part of the language.** The compiler always ANDs the channel's consent column (`consent_sms` or `consent_email`) when it builds a recipient query. No exported function returns recipients without it. Audience counts report the total and the per-channel eligible counts separately.

### Normalization

Before schema validation, enum strings (`kind`, `field`, `op`, `event`, `match`, enum values) are lowercased, ASCII letters only. The structured-outputs docs warn that enum values can come back with different capitalization. Non-ASCII input such as `İSTANBUL` is left as is, so it still fails validation, loudly.

## Known limitation: windows are independent

Each event condition looks at its own window, and conditions cannot refer to each other. "Added to cart and did not buy afterwards" is therefore not expressible in v1.

The spec's example uses `cart_add within 30` AND `purchase within 30, negate`. That rule drops a user who bought 25 days ago and then added to cart 5 days ago, even though that user is a real abandoner. The error only goes one way: any purchase in the window excludes the user, even one made before the cart add, so v1 misses some abandoners but never includes someone who bought after adding to cart.

A sequence condition (`event A not followed by event B within N days`) is the v2 candidate. Tests and the Go oracle implement the v1 semantics above, not the intent behind the seed data. `DECISIONS.md` records the trade-off.

## Errors

The validator returns every problem it finds, each as `{path, code, message}`. `path` is a JSON Pointer into the rule, for example `/conditions/1/within_days`. Codes are stable and are also used as metric labels.

| Code | When |
|---|---|
| `invalid_json` | the body is not exactly one JSON value |
| `required` | a required key is missing |
| `unknown_property` | a key the schema does not define |
| `invalid_type` | wrong JSON type, for example `"30"` for an integer |
| `unknown_value` | a value outside the catalog: unknown version, kind, field, operator, event or slug (`x'; DROP TABLE users;--` as a city ends here) |
| `op_not_allowed` | the operator does not exist for that field, for example `gt` on `city` |
| `op_value_mismatch` | a list operator with one value, or a single operator with a list |
| `out_of_range` | a number outside its range, for example `within_days: 0` |
| `too_few` / `too_many` | conditions or list values outside their size limits |
| `duplicate_value` | the same value twice in a list |

## Worked example

Brief from the case spec: send a 30% discount reminder to Istanbul users who added items to their cart in the last 30 days but did not buy. `docs/examples/istanbul_cart_abandoners.json` holds the rule, and a test keeps it valid:

```json
{
  "version": 1,
  "match": "all",
  "conditions": [
    { "kind": "enum_attribute", "field": "city", "op": "eq", "value": "istanbul" },
    { "kind": "event", "event": "cart_add", "within_days": 30, "negate": false },
    { "kind": "event", "event": "purchase", "within_days": 30, "negate": true }
  ]
}
```

`segment.AudienceQuery` turns it into this for the SMS channel. Line breaks are added here; `TestCompileExample` pins the exact text.

```sql
SELECT u.id FROM users u WHERE (u.city = $1
  AND EXISTS (SELECT 1 FROM user_events e WHERE e.user_id = u.id AND e.type = $2
              AND e.occurred_at >= $3 AND e.occurred_at <= $4)
  AND NOT EXISTS (SELECT 1 FROM user_events e WHERE e.user_id = u.id AND e.type = $5
              AND e.occurred_at >= $6 AND e.occurred_at <= $7))
  AND u.consent_sms
-- args: istanbul, cart_add, asOf-30d, asOf, purchase, asOf-30d, asOf
```

- Every value, event type and time cutoff is a bound parameter, and column names and operators come from Go maps. The SQL text depends only on the rule's shape, never on its values. `TestShapeProperty` and `FuzzCompileShape` check that.
- The rule is always wrapped in parentheses, and the consent column is ANDed outside them. So an `any` rule (OR) cannot leak past the consent filter.
- Only `AudienceQuery`, which always adds consent, and `CountAudience` are exported. Nothing returns recipients without the filter.
- On the seed data, `TestOracle` checks 500 random rules against a Go implementation of these semantics. `TestBoundaries` pins the exact window and day edges.
