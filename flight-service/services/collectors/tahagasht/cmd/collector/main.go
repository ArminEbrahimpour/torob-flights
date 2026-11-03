package main

import (
	"context"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/joho/godotenv"

	"github.com/airbar/flight-service/services/collectors/tahagasht/internal/collect"
	"github.com/airbar/flight-service/services/collectors/tahagasht/internal/config"
	"github.com/airbar/flight-service/services/collectors/tahagasht/internal/jobs"
	"github.com/airbar/flight-service/services/collectors/tahagasht/internal/tahagasht"
	"github.com/airbar/flight-service/shared/pkg/collectorboot"
	"github.com/airbar/flight-service/shared/pkg/env"
	"github.com/airbar/flight-service/shared/pkg/logging"
)

func main() {
	_ = godotenv.Load()
	cfg := config.Load()
	logging.Setup(cfg.Environment, cfg.LogLevel)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	err := collectorboot.Run(
		ctx,
		"tahagasht-collector",
		cfg.DatabaseURL,
		cfg.MetricsAddr,
		[]string{cfg.MigrationsPath, cfg.MultiCollectorMigrationsPath},
		0,
		func(ctx context.Context, pool *pgxpool.Pool) error {
			store := jobs.NewStore(pool)
			client := tahagasht.New(tahagasht.Options{
				Adult:                   cfg.Adult,
				FlightClass:             cfg.FlightClass,
				RPS:                     cfg.RPS,
				Burst:                   cfg.Burst,
				HTTPTimeout:             cfg.HTTPTimeout,
				UpstreamPollInterval:    cfg.UpstreamPollInterval,
				MaxPolls:                cfg.MaxPolls,
				CircuitBreakerThreshold: cfg.CircuitBreakerThreshold,
				CircuitBreakerOpen:      cfg.CircuitBreakerOpen,
			})
			engine := collect.New(cfg, store, client)

			slog.Info("tahagasht collector starting",
				"workers", cfg.MaxWorkers,
				"rps", cfg.RPS,
				"upstream_poll_interval", cfg.UpstreamPollInterval,
				"database", env.GetString("DATABASE_URL", ""),
			)
			if err := engine.Run(ctx); err != nil && err != context.Canceled {
				return err
			}
			slog.Info("tahagasht collector stopped")
			return nil
		},
	)
	if err != nil {
		slog.Error("tahagasht collector failed", "error", err)
		os.Exit(1)
	}
}
