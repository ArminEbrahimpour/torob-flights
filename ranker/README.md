# Torob flight ranker

Consumes flight upsert events from RabbitMQ, mirrors the offers into its own
PostgreSQL database (`ranker_db`), and periodically ranks offers sharing the
same origin, destination, and date.

Ranking order is deterministic: lowest price first, then more baggage, earlier
departure, then airline code. Results are served by `GET /v1/rankings/flights`.

```bash
docker network create torob-net
docker compose up -d --build
curl 'http://localhost:8081/v1/rankings/flights?origin=IKA&destination=MHD&date=2026-11-10'
```
