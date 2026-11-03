FROM golang:1.23-bookworm AS build
WORKDIR /src
COPY go.mod ./
COPY . .
RUN CGO_ENABLED=0 go build -o /core-api ./cmd/core-api

FROM gcr.io/distroless/static-debian12
COPY --from=build /core-api /core-api
EXPOSE 8080
ENTRYPOINT ["/core-api"]
