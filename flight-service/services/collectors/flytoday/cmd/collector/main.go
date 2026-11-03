package main

import (
	"context"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/joho/godotenv"

	"github.com/airbar/flight-service/services/collectors/flytoday/internal/collect"
	"github.com/airbar/flight-service/services/collectors/flytoday/internal/config"
	"github.com/airbar/flight-service/services/collectors/flytoday/internal/flytoday"
	"github.com/airbar/flight-service/services/collectors/flytoday/internal/jobs"
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
		"flytoday-collector",
		cfg.DatabaseURL,
		cfg.MetricsAddr,
		[]string{cfg.MigrationsPath, cfg.MultiCollectorMigrationsPath},
		0,
		func(ctx context.Context, pool *pgxpool.Pool) error {
			store := jobs.NewStore(pool)
			client := flytoday.New(flytoday.Options{
				Adult:                   cfg.Adult,
				CabinType:               cfg.CabinType,
				RPS:                     cfg.RPS,
				Burst:                   cfg.Burst,
				HTTPTimeout:             cfg.HTTPTimeout,
				CircuitBreakerThreshold: cfg.CircuitBreakerThreshold,
				CircuitBreakerOpen:      cfg.CircuitBreakerOpen,
				BaseURL:                 cfg.BaseURL,
				Referer:                 cfg.Referer,
				OriginHeader:            cfg.OriginHeader,
				RequestSource:           cfg.RequestSource,
				Cookie:                  cfg.Cookie,
				PollInterval:            cfg.PollInterval,
				MaxPolls:                cfg.MaxPolls,
			})
			engine := collect.New(cfg, store, client)

			slog.Info("flytoday collector starting",
				"workers", cfg.MaxWorkers,
				"rps", cfg.RPS,
				"collect_job_spacing", cfg.CollectJobSpacing,
				"database", env.GetString("DATABASE_URL", ""),
			)
			if err := engine.Run(ctx); err != nil && err != context.Canceled {
				return err
			}
			slog.Info("flytoday collector stopped")
			return nil
		},
	)
	if err != nil {
		slog.Error("flytoday collector failed", "error", err)
		os.Exit(1)
	}
}
