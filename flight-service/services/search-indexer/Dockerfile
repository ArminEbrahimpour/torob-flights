ARG DOCKER_PULL_REGISTRY=docker.airbar.tech
FROM ${DOCKER_PULL_REGISTRY}/library/golang:1.24-bookworm AS binary
WORKDIR /build
ENV CGO_ENABLED=0 GOFLAGS=-mod=vendor
COPY go.mod go.sum ./
COPY vendor/ ./vendor/
COPY . .
RUN go build -o /build/flight-search-indexer ./services/search-indexer/cmd/indexer

FROM ${DOCKER_PULL_REGISTRY}/library/debian:bookworm-slim
RUN apt-get update && apt-get install -y --no-install-recommends ca-certificates && rm -rf /var/lib/apt/lists/*
WORKDIR /app
COPY --from=binary /build/flight-search-indexer /app/flight-search-indexer
COPY migrations /app/migrations
CMD ["/app/flight-search-indexer"]
