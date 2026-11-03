package main

import (
	"context"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/torob/ranker/internal/adapters/httpapi"
	"github.com/torob/ranker/internal/adapters/postgres"
	"github.com/torob/ranker/internal/adapters/rabbitmq"
	"github.com/torob/ranker/internal/domain"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"
)

func env(k, d string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return d
}
func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	dbURL := env("RANKER_DATABASE_URL", "postgres://ranker:ranker@localhost:5434/ranker_db?sslmode=disable")
	pool, err := pgxpool.New(ctx, dbURL)
	if err != nil {
		panic(err)
	}
	defer pool.Close()
	for _, sql := range []string{"CREATE TABLE IF NOT EXISTS schema_migrations(name TEXT PRIMARY KEY);"} {
		if _, err := pool.Exec(ctx, sql); err != nil {
			panic(err)
		}
	}
	schema := `CREATE TABLE IF NOT EXISTS flights (id BIGINT PRIMARY KEY,origin TEXT NOT NULL,destination TEXT NOT NULL,flight_date DATE NOT NULL,airline_code TEXT NOT NULL,airline_name TEXT NOT NULL DEFAULT '',flight_number TEXT NOT NULL,depart_at TIMESTAMPTZ NOT NULL,arrive_at TIMESTAMPTZ NOT NULL,depart_minute INT NOT NULL DEFAULT 0,duration_min INT,baggage_label TEXT NOT NULL DEFAULT '',baggage_kg INT,price_rial BIGINT NOT NULL DEFAULT 0,price_toman BIGINT NOT NULL DEFAULT 0,is_charter BOOLEAN NOT NULL DEFAULT FALSE,stops INT NOT NULL DEFAULT 0,source TEXT NOT NULL DEFAULT '',collected_at TIMESTAMPTZ NOT NULL DEFAULT now(),updated_at TIMESTAMPTZ NOT NULL DEFAULT now());ALTER TABLE flights ADD COLUMN IF NOT EXISTS arrive_at TIMESTAMPTZ;ALTER TABLE flights ADD COLUMN IF NOT EXISTS duration_min INT;UPDATE flights SET arrive_at=depart_at WHERE arrive_at IS NULL;CREATE INDEX IF NOT EXISTS idx_ranker_flights_route_date ON flights(origin,destination,flight_date);CREATE TABLE IF NOT EXISTS ranked_flights (flight_id BIGINT PRIMARY KEY REFERENCES flights(id) ON DELETE CASCADE,origin TEXT NOT NULL,destination TEXT NOT NULL,flight_date DATE NOT NULL,rank INT NOT NULL,score DOUBLE PRECISION NOT NULL,ranked_at TIMESTAMPTZ NOT NULL DEFAULT now());CREATE INDEX IF NOT EXISTS idx_ranked_flights_route_date ON ranked_flights(origin,destination,flight_date,rank);`
	if _, err := pool.Exec(ctx, schema); err != nil {
		panic(err)
	}
	store := postgres.New(pool)
	consumer := rabbitmq.New(env("RABBITMQ_URL", "amqp://guest:guest@localhost:5672/"), env("RANKER_RABBITMQ_EXCHANGE", "torob.flights"), env("RANKER_RABBITMQ_QUEUE", "torob.ranker"), env("RANKER_RABBITMQ_ROUTING_KEY", "flight.upsert"))
	go func() {
		for ctx.Err() == nil {
			if err := consumer.Consume(ctx, func(c context.Context, f domain.Flight) error {
				if err := store.Upsert(c, f); err != nil {
					slog.Error("ranker flight upsert", "flight_id", f.ID, "origin", f.Origin, "destination", f.Destination, "error", err)
					return err
				}
				return nil
			}); err != nil {
				slog.Warn("rabbit consumer", "error", err)
				time.Sleep(3 * time.Second)
			}
		}
	}()
	interval := duration("RANKER_INTERVAL", 10*time.Second)
	go func() {
		t := time.NewTicker(interval)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				if err := store.Rebuild(ctx); err != nil {
					slog.Warn("rank rebuild", "error", err)
				}
			}
		}
	}()
	server := &http.Server{Addr: env("HTTP_ADDR", ":8081"), Handler: httpapi.New(store).Router()}
	go func() {
		if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			slog.Error("ranker api", "error", err)
			stop()
		}
	}()
	<-ctx.Done()
	shutdown, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = server.Shutdown(shutdown)
}
func duration(k string, d time.Duration) time.Duration {
	v := os.Getenv(k)
	if v == "" {
		return d
	}
	x, e := time.ParseDuration(v)
	if e != nil {
		return d
	}
	return x
}
