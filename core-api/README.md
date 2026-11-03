# Torob Core API

The product-facing API for the flight MVP. It owns the public contract and delegates flight-domain search to `flight-service`; clients never call Elasticsearch directly.

Architecture:

```text
client -> core-api (inbound HTTP adapter) -> FlightSearch port -> flight-service adapter -> flight-service -> Elasticsearch/PostgreSQL
```

Ranked results are available through `GET /v1/flights/ranked`; the core API
delegates that request to the ranker service using `RANKER_URL`.

Run locally:

```bash
FLIGHT_SERVICE_URL=http://localhost:8090 go run ./cmd/core-api
curl 'http://localhost:8080/v1/flights?origin=IKA&destination=MHD&date=2026-11-10'
```

Run with Docker Compose:

```bash
docker compose up -d --build
```

Set `FLIGHT_SERVICE_URL` to the reachable flight-service hostname. Create the
shared network once before starting the separate stacks:

```bash
docker network create torob-net
```

Configuration: `HTTP_ADDR`, `FLIGHT_SERVICE_URL`, `RANKER_URL`, and `REQUEST_TIMEOUT`.
