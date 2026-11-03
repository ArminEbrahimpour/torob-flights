# Architecture

## Outer architecture (modulith)

```text
Internet → airbar-core → flight-services-api (internal HTTP)
                              ├─ /v1/flights/*
                              ├─ /v1/pnr/*
                              └─ /v1/tickets/* (stub)

flight-services-*-collector (worker) → PostgreSQL ← flight-services-api reads

PostgreSQL stores every normalized provider offer. Collectors do not use Kafka for
flight-offer publication; Kafka is not part of the flight-service collection path.
PNR cache → Redis (optional, api only)
```

## Inner architecture

### flight-services-api HTTP domains

`handler → service → repository`

Packages:

- `internal/http/<domain>/handler.go`
- `internal/<domain>/service.go`
- `internal/<domain>/postgres.go`

### PNR checkers (in-process)

`pnr/service.go → registry → checkers/<airline>/`

Each checker implements:

```go
type Checker interface {
    Airline() string
    Check(ctx context.Context, req VerifyRequest) (CheckResult, error)
}
```

### Collectors (workers)

`collect/service.go → upstream client + jobs/postgres store`

No HTTP business endpoints.

## Contracts

- `contracts/http/flight-api.openapi.yaml` — airbar-core ↔ flight-services-api
- `contracts/schemas/normalized-flight.json` — collector DB row shape
- `contracts/collector/README.md` — collector rules

## Adding features

See `docs/ADDING-A-PNR-CHECKER.md`.
