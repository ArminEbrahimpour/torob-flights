FROM golang:1.23-bookworm AS build
WORKDIR /src
COPY go.mod ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -o /ranker ./cmd/ranker
FROM gcr.io/distroless/static-debian12
COPY --from=build /ranker /ranker
EXPOSE 8081
ENTRYPOINT ["/ranker"]
