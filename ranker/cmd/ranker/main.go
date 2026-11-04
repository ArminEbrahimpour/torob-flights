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
	"net/url"
	"os"
	"os/signal"
	"strings"
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
	dbURL := env("RANKER_DATABASE_URL", "postgres://ranker:ranker@127.0.0.1:5433/ranker_db?sslmode=disable")
	var pool *pgxpool.Pool
	var err error
	for ctx.Err() == nil {
		pool, err = pgxpool.New(ctx, dbURL)
		if err == nil {
			err = pool.Ping(ctx)
		}
		if err == nil {
			break
		}
		if pool != nil {
			pool.Close()
			pool = nil
		}
		slog.Error("ranker database unavailable; retrying", "database", redactDatabaseURL(dbURL), "retry_in", "5s", "error", err)
		select {
		case <-ctx.Done():
			return
		case <-time.After(5 * time.Second):
		}
	}
	if pool == nil {
		return
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
	for _, alter := range []string{
		`ALTER TABLE ranked_flights ADD COLUMN IF NOT EXISTS price_score DOUBLE PRECISION NOT NULL DEFAULT 0`,
		`ALTER TABLE ranked_flights ADD COLUMN IF NOT EXISTS duration_score DOUBLE PRECISION NOT NULL DEFAULT 0`,
		`ALTER TABLE ranked_flights ADD COLUMN IF NOT EXISTS stops_score DOUBLE PRECISION NOT NULL DEFAULT 0`,
		`ALTER TABLE ranked_flights ADD COLUMN IF NOT EXISTS baggage_score DOUBLE PRECISION NOT NULL DEFAULT 0`,
		`ALTER TABLE flights ADD COLUMN IF NOT EXISTS offer_url TEXT NOT NULL DEFAULT ''`,
	} {
		if _, err := pool.Exec(ctx, alter); err != nil {
			panic(err)
		}
	}
	store := postgres.New(pool)
	rabbitURL := env("RABBITMQ_URL", "amqp://torob:torob@127.0.0.1:5672/")
	consumer := rabbitmq.New(rabbitURL, env("RANKER_RABBITMQ_EXCHANGE", "torob.flights"), env("RANKER_RABBITMQ_QUEUE", "torob.ranker"), env("RANKER_RABBITMQ_ROUTING_KEY", "flight.upsert"))
	go func() {
		for ctx.Err() == nil {
			if err := consumer.Consume(ctx, func(c context.Context, f domain.Flight) error {
				if err := store.Upsert(c, f); err != nil {
					slog.Error("ranker flight upsert", "flight_id", f.ID, "origin", f.Origin, "destination", f.Destination, "error", err)
					return err
				}
				return nil
			}); err != nil {
				slog.Warn("rabbit consumer", "error", err, "user", amqpUser(rabbitURL))
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

func amqpUser(raw string) string {
	parsed, err := url.Parse(raw)
	if err != nil || parsed.User == nil || parsed.User.Username() == "" {
		return "unknown"
	}
	return parsed.User.Username()
}
func redactDatabaseURL(value string) string {
	// Connection endpoints are useful diagnostics; credentials are never logged.
	if at := strings.LastIndex(value, "@"); at >= 0 {
		return "postgres://***@" + value[at+1:]
	}
	return "postgres://***"
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
