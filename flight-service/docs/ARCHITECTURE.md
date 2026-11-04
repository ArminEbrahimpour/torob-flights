# Flight-service

```mermaid
flowchart TB
  API[HTTP API] --> DB[(PostgreSQL)]
  API --> ES[(Elasticsearch)]
  COL[provider collectors] --> JOBS[jobs + routes]
  COL --> DB
  DB --> SO[search outbox] --> IDX[search-indexer] --> ES
  DB --> RO[ranker outbox] --> PUB[ranker-publisher] --> MQ[(RabbitMQ)]
```

Collectors normalize provider responses into `shared/domain/flight.Flight`.
Each upsert triggers durable search and ranker outbox rows. Jobs are
provider-scoped, retry with backoff, reclaim stale locks, and record collection
runs. The API filters stale offers and enqueues asynchronous refresh jobs when
no fresh result exists.

## Main tables

`airports`, `routes`, `jobs`, `collector_route_state`, `flights`,
`collection_runs`, `flight_search_outbox`, and `flight_ranker_outbox` are
defined in `migrations/001` through `008`.

## Logging

Collectors log startup, migration, route seeding, job claim/defer/failure,
provider calls, offer counts, upserts, circuit state, and shutdown. Include
`source`, `worker`, `job`, `route`, `date`, `attempt`, `flights`, and `duration`.
