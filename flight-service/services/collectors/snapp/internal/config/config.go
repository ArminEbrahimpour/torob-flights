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
	CabinType                    string
	HTTPTimeout                  time.Duration
	CircuitBreakerThreshold      int
	CircuitBreakerOpen           time.Duration
	RateLimitBackoffBase         time.Duration
	RateLimitBackoffMax          time.Duration
	TransientBackoffBase         time.Duration
	TransientBackoffMax          time.Duration
	CollectJobSpacing            time.Duration
	BaseURL                      string
	Referer                      string
	OriginHeader                 string
	RequestSource                string
	Cookie                       string
	MetricsAddr                  string
	Environment                  string
	LogLevel                     string
}

func Load() Config {
	return Config{
		DatabaseURL:                  env.GetString("DATABASE_URL", "postgres://flight:flight@localhost:5432/airbar_flights?sslmode=disable"),
		MigrationsPath:               env.GetString("MIGRATIONS_PATH", "migrations/001_flights_core.sql"),
		MultiCollectorMigrationsPath: env.GetString("MULTI_COLLECTOR_MIGRATION_PATH", "migrations/002_multi_collector.sql"),
		MaxWorkers:                   env.MaxInt(1, env.GetInt("MAX_WORKERS", 1)),
		RPS:                          env.PositiveFloat(env.GetFloat("RATE_LIMIT_RPS", 0.15), 0.15),
		Burst:                        env.MaxInt(1, env.GetInt("RATE_LIMIT_BURST", 1)),
		PollInterval:                 env.GetDuration("POLL_INTERVAL", time.Second),
		SeedInterval:                 env.GetDuration("SEED_INTERVAL", 3*time.Minute),
		JobStaleAfter:                env.GetDuration("JOB_STALE_AFTER", 15*time.Minute),
		Adult:                        env.MaxInt(1, env.GetInt("ADULT", 1)),
		CabinType:                    env.GetString("CABIN_TYPE", "ECONOMY"),
		HTTPTimeout:                  env.GetDuration("HTTP_TIMEOUT", 60*time.Second),
		CircuitBreakerThreshold:      env.MaxInt(1, env.GetInt("CIRCUIT_BREAKER_THRESHOLD", 2)),
		CircuitBreakerOpen:           env.GetDuration("CIRCUIT_BREAKER_OPEN", 15*time.Minute),
		RateLimitBackoffBase:         env.GetDuration("RATE_LIMIT_BACKOFF_BASE", 2*time.Minute),
		RateLimitBackoffMax:          env.GetDuration("RATE_LIMIT_BACKOFF_MAX", 45*time.Minute),
		TransientBackoffBase:         env.GetDuration("TRANSIENT_BACKOFF_BASE", 30*time.Second),
		TransientBackoffMax:          env.GetDuration("TRANSIENT_BACKOFF_MAX", 10*time.Minute),
		CollectJobSpacing:            env.GetDuration("COLLECT_JOB_SPACING", 8*time.Second),
		BaseURL:                      env.GetString("SNAPP_BASE_URL", ""),
		Referer:                      env.GetString("SNAPP_REFERER", ""),
		OriginHeader:                 env.GetString("SNAPP_ORIGIN", ""),
		RequestSource:                env.GetString("SNAPP_REQUEST_SOURCE", "searchBox"),
		Cookie:                       env.GetString("SNAPP_COOKIE", ""),
		MetricsAddr:                  env.GetString("METRICS_ADDR", ":9093"),
		Environment:                  env.GetString("APP_ENV", "development"),
		LogLevel:                     env.GetString("LOG_LEVEL", "info"),
	}
}
