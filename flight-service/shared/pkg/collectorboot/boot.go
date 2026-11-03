package collectorboot

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"path/filepath"
	"sync/atomic"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/airbar/flight-service/shared/pkg/postgres"
)

// DefaultDBConns is the per-collector Postgres pool size. Collectors only need a
// handful of connections (claim job, upsert, maintenance); keeping this low
// avoids exhausting the shared airbar-postgres max_connections budget.
const DefaultDBConns int32 = 2

// Run connects to Postgres, starts the health server immediately, applies
// migrations once, then invokes run with a live connection pool.
func Run(
	ctx context.Context,
	serviceName string,
	databaseURL string,
	metricsAddr string,
	migrationPaths []string,
	maxConns int32,
	run func(ctx context.Context, pool *pgxpool.Pool) error,
) error {
	if maxConns <= 0 {
		maxConns = DefaultDBConns
	}
	pool, err := postgres.Connect(ctx, databaseURL, maxConns)
	if err != nil {
		return fmt.Errorf("database connect: %w", err)
	}
	defer pool.Close()

	var ready atomic.Bool
	if metricsAddr != "" {
		startHealth(metricsAddr, serviceName, pool.Pool, &ready)
	}

	paths := make([]string, 0, len(migrationPaths)+1)
	for _, configuredPath := range migrationPaths {
		if configuredPath == "" {
			continue
		}
		migPath := configuredPath
		if abs, err := filepath.Abs(migPath); err == nil {
			migPath = abs
		}
		paths = append(paths, migPath)
	}
	paths = postgres.AppendSiblingMigration(paths, "004_maintenance_indexes.sql")
	paths = postgres.AppendSiblingMigration(paths, "005_offer_identity.sql")
	paths = postgres.AppendSiblingMigration(paths, "006_flight_search_outbox.sql")
	paths = postgres.AppendSiblingMigration(paths, "007_ranker_outbox.sql")
	paths = postgres.AppendSiblingMigration(paths, "008_iran_domestic_routes.sql")
	if err := postgres.RunMigrations(ctx, pool.Pool, paths); err != nil {
		return err
	}
	ready.Store(true)

	return run(ctx, pool.Pool)
}

func startHealth(addr, serviceName string, pool *pgxpool.Pool, ready *atomic.Bool) {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /health/live", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"status":"ok","service":"` + serviceName + `"}`))
	})
	mux.HandleFunc("GET /health/ready", func(w http.ResponseWriter, r *http.Request) {
		if !ready.Load() {
			http.Error(w, "migrations pending", http.StatusServiceUnavailable)
			return
		}
		if err := pool.Ping(r.Context()); err != nil {
			http.Error(w, "postgres unavailable", http.StatusServiceUnavailable)
			return
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"status":"ready"}`))
	})
	go func() {
		slog.Info("collector health server listening", "addr", addr, "service", serviceName)
		if err := http.ListenAndServe(addr, mux); err != nil && err != http.ErrServerClosed {
			slog.Error("health server failed", "error", err, "service", serviceName)
		}
	}()
}
