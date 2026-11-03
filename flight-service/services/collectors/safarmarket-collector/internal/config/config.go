package config

import (
	"time"

	"github.com/airbar/flight-service/shared/pkg/env"
)

type Config struct {
	DatabaseURL                  string
	MigrationsPath               string
	MultiCollectorMigrationsPath string
	MaxWorkers                   int
	RPS                          float64
	Burst                        int
	PollInterval                 time.Duration
	SeedInterval                 time.Duration
	JobStaleAfter                time.Duration
	Adult                        int
	FlightClass                  string
	HTTPTimeout                  time.Duration
	CircuitBreakerThreshold      int
	CircuitBreakerOpen           time.Duration
	RateLimitBackoffBase         time.Duration
	RateLimitBackoffMax          time.Duration
	TransientBackoffBase         time.Duration
	TransientBackoffMax          time.Duration
	CollectJobSpacing            time.Duration
	MetricsAddr                  string
	Environment                  string
	LogLevel                     string
}

func Load() Config {
	return Config{
		DatabaseURL:                  env.GetString("DATABASE_URL", "postgres://postgres:postgres@localhost:5432/flighty?sslmode=disable"),
		MigrationsPath:               env.GetString("MIGRATIONS_PATH", "migrations/001_flights_core.sql"),
		MultiCollectorMigrationsPath: env.GetString("MULTI_COLLECTOR_MIGRATION_PATH", "migrations/002_multi_collector.sql"),
		MaxWorkers:                   env.MaxInt(1, env.GetInt("MAX_WORKERS", 1)),
		RPS:                          env.PositiveFloat(env.GetFloat("RATE_LIMIT_RPS", 0.05), 0.05),
		Burst:                        env.MaxInt(1, env.GetInt("RATE_LIMIT_BURST", 1)),
		PollInterval:                 env.GetDuration("POLL_INTERVAL", time.Second),
		SeedInterval:                 env.GetDuration("SEED_INTERVAL", 2*time.Minute),
		JobStaleAfter:                env.GetDuration("JOB_STALE_AFTER", 15*time.Minute),
		Adult:                        env.MaxInt(1, env.GetInt("ADULT", 1)),
		FlightClass:                  env.GetString("FLIGHT_CLASS", "economy"),
		HTTPTimeout:                  env.GetDuration("HTTP_TIMEOUT", 60*time.Second),
		CircuitBreakerThreshold:      env.MaxInt(1, env.GetInt("CIRCUIT_BREAKER_THRESHOLD", 3)),
		CircuitBreakerOpen:           env.GetDuration("CIRCUIT_BREAKER_OPEN", 10*time.Minute),
		RateLimitBackoffBase:         env.GetDuration("RATE_LIMIT_BACKOFF_BASE", time.Minute),
		RateLimitBackoffMax:          env.GetDuration("RATE_LIMIT_BACKOFF_MAX", 30*time.Minute),
		TransientBackoffBase:         env.GetDuration("TRANSIENT_BACKOFF_BASE", 15*time.Second),
		TransientBackoffMax:          env.GetDuration("TRANSIENT_BACKOFF_MAX", 5*time.Minute),
		CollectJobSpacing:            env.GetDuration("COLLECT_JOB_SPACING", 5*time.Second),
		MetricsAddr:                  env.GetString("METRICS_ADDR", ":9091"),
		Environment:                  env.GetString("APP_ENV", "development"),
		LogLevel:                     env.GetString("LOG_LEVEL", "info"),
	}
}
