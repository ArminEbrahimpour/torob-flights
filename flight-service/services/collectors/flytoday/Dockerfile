ARG DOCKER_PULL_REGISTRY=docker.airbar.tech
FROM ${DOCKER_PULL_REGISTRY}/library/golang:1.24-bookworm AS binary
WORKDIR /build
ENV CGO_ENABLED=0 GOFLAGS=-mod=vendor
COPY go.mod go.sum ./
COPY vendor/ ./vendor/
COPY . .
RUN go build -o /build/flight-services-flytoday-collector ./services/collectors/flytoday/cmd/collector

FROM ${DOCKER_PULL_REGISTRY}/library/debian:bookworm-slim
RUN apt-get update \
  && apt-get install -y --no-install-recommends ca-certificates wget \
  && rm -rf /var/lib/apt/lists/*
WORKDIR /app
COPY --from=binary /build/flight-services-flytoday-collector /app/flight-services-flytoday-collector
COPY migrations /app/migrations
ENV MIGRATIONS_PATH=/app/migrations/001_flights_core.sql
ENV MULTI_COLLECTOR_MIGRATION_PATH=/app/migrations/002_multi_collector.sql
EXPOSE 9096
CMD ["/app/flight-services-flytoday-collector"]
