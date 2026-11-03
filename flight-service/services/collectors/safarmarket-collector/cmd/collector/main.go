package main

import (
	"context"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/joho/godotenv"

	"github.com/airbar/flight-service/services/collectors/safarmarket-collector/internal/collect"
	"github.com/airbar/flight-service/services/collectors/safarmarket-collector/internal/config"
	"github.com/airbar/flight-service/services/collectors/safarmarket-collector/internal/jobs"
	"github.com/airbar/flight-service/services/collectors/safarmarket-collector/internal/safarmarket"
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
		"safarmarket-collector",
		cfg.DatabaseURL,
		cfg.MetricsAddr,
		[]string{cfg.MigrationsPath, cfg.MultiCollectorMigrationsPath},
		0,
		func(ctx context.Context, pool *pgxpool.Pool) error {
			store := jobs.NewStore(pool)
			client := safarmarket.New(safarmarket.Options{
				Adult:                   cfg.Adult,
				FlightClass:             cfg.FlightClass,
				RPS:                     cfg.RPS,
				Burst:                   cfg.Burst,
				HTTPTimeout:             cfg.HTTPTimeout,
				CircuitBreakerThreshold: cfg.CircuitBreakerThreshold,
				CircuitBreakerOpen:      cfg.CircuitBreakerOpen,
			})
			engine := collect.New(cfg, store, client)

			slog.Info("safarmarket collector starting",
				"workers", cfg.MaxWorkers,
				"rps", cfg.RPS,
				"database", env.GetString("DATABASE_URL", ""),
			)
			if err := engine.Run(ctx); err != nil && err != context.Canceled {
				return err
			}
			slog.Info("safarmarket collector stopped")
			return nil
		},
	)
	if err != nil {
		slog.Error("safarmarket collector failed", "error", err)
		os.Exit(1)
	}
}
