# Collector contract

Every flight data collector in `services/collectors/*` must follow these rules.

## Responsibilities

1. Collect route/date snapshots from an upstream provider.
2. Normalize offers into rows compatible with `contracts/schemas/normalized-flight.json`.
3. Upsert rows into PostgreSQL `flights` table.
4. Use the shared Postgres job queue (`jobs` table) scoped by `source`.

## Non-responsibilities

- Do not expose business HTTP APIs.
- Do not publish to Redis.
- Do not call `flight-services-api`.
- Do not implement public authentication.

## Registered sources

| `source` value | Collector service |
|---|---|
| `alibaba` | `services/collectors/alibaba` |
| `tahagasht` | `services/collectors/tahagasht` |
| `snapp` | `services/collectors/snapp` |
| `mrbilit` | `services/collectors/mrbilit-collector` |
| `safarmarket` | `services/collectors/safarmarket-collector` |
| `flytoday` | `services/collectors/flytoday` |
| `trip` | `services/collectors/trip` |

## Normalized row fields

Collectors must populate the fields below before upsert. See `contracts/schemas/normalized-flight.json` for types.

| Field | Required | Notes |
|---|---|---|
| `source` | yes | Stable lowercase identifier for this collector |
| `origin_query` / `destination_query` | yes | Query codes from `routes` (e.g. `THRALL`, `ISTALL`) |
| `origin` / `destination` | yes | Concrete airport codes when known |
| `flight_date` | yes | `YYYY-MM-DD` |
| `airline_code` | yes | IATA code |
| `airline_name` | recommended | Empty string if unknown |
| `flight_number` | yes | Raw provider value |
| `flight_number_norm` | yes | Use shared `flightnum.Normalize` / Python equivalent |
| `depart_at` / `arrive_at` | yes | RFC 3339 timestamps |
| `depart_minute` / `arrive_minute` | yes | `hour*60 + minute` |
| `baggage_label` | no | e.g. `30 KG` |
| `baggage_kg` | no | Integer kilograms when parseable |
| `price_rial` | yes | Provider price in rials |
| `price_toman` | yes | Usually `price_rial / 10` |
| `is_charter` | yes | `false` when unknown |
| `stops` | yes | `0` for nonstop |
| `duration_min` | no | Total journey minutes |
| `proposal_id` | recommended | Provider offer id; improves upsert stability |
| `collected_at` | no | Set by database on upsert |

## Job kinds

- `seed_route`: expand rolling date window into `collect_day` jobs
- `collect_day`: one provider fetch for one route/date

All job queue reads/writes must filter by the collector's `source` (see `migrations/002_multi_collector.sql`).

## Health endpoints (optional)

Collectors may expose internal-only:

- `GET /health/live`
- `GET /health/ready`

## Adding a new collector

1. Create `services/collectors/<name>/`.
2. Implement worker + upstream client + postgres store.
3. Reuse `shared/domain/flight` types (Go) or mirror `FlightRow` (Python).
4. Register a unique `source` value.
5. Scope all job queue operations to that `source`.
6. Add Dockerfile and compose service.
7. Extend `contracts/schemas/normalized-flight.json` examples only if a new field is genuinely required.
