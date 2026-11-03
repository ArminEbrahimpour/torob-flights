# flight-services

Modulith for AirBar flight intelligence and PNR verification.

GitLab: `services/flight-services` · images `flight-services-{role}:{staging|latest}`

## Layout

```text
flight-services/
├── contracts/          # OpenAPI + JSON schemas
├── migrations/         # PostgreSQL schema
├── shared/             # domain types + infra helpers
├── deploy/             # Ansible + Swarm compose templates
└── services/
    ├── api/             # flight-services-api — ONLY HTTP entry (internal)
    ├── search-indexer/  # PostgreSQL outbox -> Elasticsearch read model
    └── collectors/      # DB writers (alibaba, tahagasht, snapp, mrbilit, …)
```

## Naming

| Role | Staging (Iran) | Production |
|---|---|---|
| API | `flight-services-api-staging` | `flight-services-api` |
| Collector | `flight-services-{name}-collector-staging` | `flight-services-{name}-collector` |

All Torob services use the shared `torob-net` Docker network. No host ports are required in deployment; the core API calls `flight-services-api:8090`.

## Processes

| Process | Port | Role |
|---|---:|---|
| `flight-services-api` | 8090 | Internal HTTP for airbar-core |
| `*-collector` | 9091–9097 | Write flights to PostgreSQL (`source=<name>`) |

## Quick start (local)

```bash
cp .env.example .env
docker network create torob-net 2>/dev/null || true
docker compose up -d --build
curl -s http://localhost:8090/health/live
curl -s http://localhost:8090/v1/pnr/airlines
```

## Deploy (GitLab CI only)

| Branch | Pipeline |
|---|---|
| `develop` | none |
| `staging` | build all images + deploy Iran |
| `main` | build + deploy production (on request) |

Staging selection is driven by GitLab `ENV_STAGING`:

```text
ENABLED_COLLECTORS=alibaba
API_REPLICAS=1
DATABASE_URL=postgres://flight_services:…@postgresql-staging:5432/flight_services?sslmode=disable
REDIS_URL=redis://redis-staging:6379/5
```

PostgreSQL is the source of truth for every collected offer. Each provider offer is retained using its source, schedule, price, charter status, and proposal identifier, so multiple fares for the same flight number and date remain queryable. Collectors do not publish flight offers to Kafka.

### Search read model

Every flight insert/update writes a row to `flight_search_outbox` in the same PostgreSQL transaction. `flight-services-search-indexer` claims those rows and upserts normalized offers into Elasticsearch. The API searches Elasticsearch when `ELASTICSEARCH_URL` is set and automatically falls back to PostgreSQL if the index is unavailable or still catching up. PostgreSQL remains authoritative.

Relevant settings are `ELASTICSEARCH_URL`, `ELASTICSEARCH_FLIGHTS_INDEX`, `SEARCH_INDEXER_BATCH`, `SEARCH_INDEXER_POLL_INTERVAL`, and `SEARCH_INDEXER_RETRY_DELAY`.

Search freshness is enforced at the read boundary: offers collected within 5
minutes are accepted for flights departing today/tomorrow, within 15 minutes
for flights in the next week, and within 60 minutes for later flights. Older
offers are excluded from both PostgreSQL and Elasticsearch search results.
Responses include `freshness_max_age_seconds` and `freshness_as_of`.

When a dated route has no fresh offers, the API also requests deduplicated,
high-priority `collect_day` jobs for the collectors and returns immediately.
The recollection happens asynchronously; clients should retry after the
collector completes. `refresh_requested` indicates whether a new job was
queued.

### Alibaba and its certificate

`ws.alibaba.ir` serves an expired certificate (notAfter 2025-12-31) to the Iran host, while other networks get the renewed one (checked 2026-10-19: the same IP, 45.89.201.11, answers differently by client). With the strict default every job fails with `x509: certificate has expired`.

`ALIBABA_ACCEPT_EXPIRED_CERT` makes the Alibaba collector forgive that one failure. It still checks the host name, the signature chain to a trusted root, and that the intermediates are valid today. A wrong name, an unknown issuer, or a certificate that is not valid yet is refused. The collector logs a warning at most once an hour while it is in use.

The deploy defaults it to `true` on staging and `false` on production. A CI/CD variable or a line in `ENV_STAGING` / `ENV_PRODUCTION` overrides the default. Set it to `false` once Alibaba serves a valid certificate to the host.

See `deploy/ENV_STAGING.example` and `deploy/create-db-staging.sql`.

Host paths: `/srv/preproduction/flight-services/staging` · `/srv/production/flight-services/production`.

## Architecture rules

- **airbar-core** is the only caller of `flight-services-api`.
- Collectors write **PostgreSQL only** (no Redis publish).
- PNR checkers are in-process under `services/api/internal/pnr/checkers/`.

See `docs/ARCHITECTURE.md`.
