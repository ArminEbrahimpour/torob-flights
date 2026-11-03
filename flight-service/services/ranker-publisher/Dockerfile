ARG DOCKER_PULL_REGISTRY=docker.airbar.tech
FROM ${DOCKER_PULL_REGISTRY}/library/golang:1.24-bookworm AS binary
WORKDIR /build
ENV CGO_ENABLED=0 GOFLAGS=-mod=vendor
COPY go.mod go.sum ./
COPY vendor/ ./vendor/
COPY . .
RUN go build -o /build/ranker-publisher ./services/ranker-publisher/cmd/publisher
FROM ${DOCKER_PULL_REGISTRY}/library/debian:bookworm-slim
WORKDIR /app
COPY --from=binary /build/ranker-publisher /app/ranker-publisher
COPY migrations /app/migrations
CMD ["/app/ranker-publisher"]
