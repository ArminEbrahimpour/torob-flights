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
external shared network. Start them with the same env file so a password
change cannot drift:

```bash
cd ../flight-service && docker compose --env-file ../infra/.env up -d --force-recreate
cd ../ranker && docker compose --env-file ../infra/.env up -d --force-recreate
cd ../core-api && docker compose --env-file ../infra/.env up -d
cd ../flight-front && docker compose --env-file ../infra/.env up -d
```

PostgreSQL and RabbitMQ rewrite credentials from their own environment on
every container start, including the Postgres superuser password. Persistent
volumes therefore cannot keep a stale password. `RABBITMQ_DEFAULT_USER` and
`RABBITMQ_DEFAULT_PASS` alone are not enough: RabbitMQ applies them only when
the data volume is first created. The broker entrypoint updates the stored
user afterwards and removes `guest`, which cannot log in from another
container (`username or password not allowed`). Postgres allows passwordless
connections only over the container-local socket so that rewrite can succeed;
TCP connections still require the configured password.

RabbitMQ management login is `RABBITMQ_USER` / `RABBITMQ_PASSWORD` (`torob` /
`torob` by default). Redis has no password; application URLs must not send
one. Elasticsearch security is disabled. Application compose files build
connection URLs from `FLIGHT_DB_*`, `RANKER_DB_*`, and `RABBITMQ_USER` /
`RABBITMQ_PASSWORD`. They do not reuse a host `RABBITMQ_URL` or `DATABASE_URL`,
so a local `guest:guest` value cannot be injected into a container.

For non-development deployments, set `FLIGHT_DB_PASSWORD`,
`RANKER_DB_PASSWORD`, `RABBITMQ_PASSWORD`, and pass `infra/.env` to every
application compose project. Passwords must be URL-safe (`A-Z`, `a-z`, `0-9`).
