# Torob infrastructure

This project owns the shared stateful services used by the Torob flight
applications:

- PostgreSQL (`flight_services` and `ranker_db` databases)
- Redis
- RabbitMQ
- Elasticsearch
- Prometheus
- Grafana
- The shared `torob-net` Docker network

Start infrastructure first:

```bash
cp .env.example .env
./up.sh
```

`up.sh` creates `torob-net` before Compose starts, which avoids the common
“declared as external, but could not be found” error.

Then start the application projects in their own directories:

```bash
cd ../flight-service && docker compose up -d
cd ../ranker && docker compose up -d
cd ../core-api && docker compose up -d
cd ../flight-front && docker compose up -d
```

The application compose files use the infrastructure service hostnames on the
external shared network. PostgreSQL initialization runs only when its volume
is created for the first time.

If PostgreSQL was already initialized with a different password, update the
role once from the running database container (this does not delete data):

```bash
docker exec -it torob-postgres psql -U postgres -d postgres \\
  -c "ALTER ROLE flight PASSWORD 'flight';"
docker compose -f ../flight-service/docker-compose.yml up -d --force-recreate
```

For non-development deployments, set `FLIGHT_DB_PASSWORD`,
`RANKER_DB_PASSWORD`, and the matching application connection URLs to your
own secrets instead of using these local defaults.
