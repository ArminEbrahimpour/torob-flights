package config

import (
	"strings"
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
	PageSize                     int
	HTTPTimeout                  time.Duration
	CircuitBreakerThreshold      int
	CircuitBreakerOpen           time.Duration
	RateLimitBackoffBase         time.Duration
	RateLimitBackoffMax          time.Duration
	TransientBackoffBase         time.Duration
	TransientBackoffMax          time.Duration
	CollectJobSpacing            time.Duration
	BaseURL                      string
	SiteURL                      string
	Cookie                       string
	MetricsAddr                  string
	Environment                  string
	LogLevel                     string
}

func Load() Config {
	flightClass := env.GetString("FLIGHT_CLASS", env.GetString("TRIP_FLIGHT_CLASS", "economy"))
	return Config{
		DatabaseURL:                  env.GetString("DATABASE_URL", "postgres://flight:flight@localhost:5432/airbar_flights?sslmode=disable"),
		MigrationsPath:               env.GetString("MIGRATIONS_PATH", "migrations/001_flights_core.sql"),
		MultiCollectorMigrationsPath: env.GetString("MULTI_COLLECTOR_MIGRATION_PATH", "migrations/002_multi_collector.sql"),
		MaxWorkers:                   env.MaxInt(1, env.GetInt("TRIP_MAX_WORKERS", env.GetInt("MAX_WORKERS", 1))),
		RPS:                          env.PositiveFloat(env.GetFloat("TRIP_RATE_LIMIT_RPS", env.GetFloat("RATE_LIMIT_RPS", 0.15)), 0.15),
		Burst:                        env.MaxInt(1, env.GetInt("TRIP_RATE_LIMIT_BURST", env.GetInt("RATE_LIMIT_BURST", 1))),
		PollInterval:                 env.GetDuration("TRIP_POLL_INTERVAL", env.GetDuration("POLL_INTERVAL", 2*time.Second)),
		SeedInterval:                 env.GetDuration("TRIP_SEED_INTERVAL", env.GetDuration("SEED_INTERVAL", 3*time.Minute)),
		JobStaleAfter:                env.GetDuration("TRIP_JOB_STALE_AFTER", env.GetDuration("JOB_STALE_AFTER", 15*time.Minute)),
		Adult:                        env.MaxInt(1, env.GetInt("TRIP_ADULT", env.GetInt("ADULT", 1))),
		CabinType:                    mapCabinType(flightClass),
		PageSize:                     env.MaxInt(1, env.GetInt("TRIP_PAGE_SIZE", 30)),
		HTTPTimeout:                  env.GetDuration("TRIP_HTTP_TIMEOUT", env.GetDuration("HTTP_TIMEOUT", 90*time.Second)),
		CircuitBreakerThreshold:      env.MaxInt(1, env.GetInt("TRIP_CIRCUIT_BREAKER_THRESHOLD", env.GetInt("CIRCUIT_BREAKER_THRESHOLD", 3))),
		CircuitBreakerOpen:           env.GetDuration("TRIP_CIRCUIT_BREAKER_OPEN", env.GetDuration("CIRCUIT_BREAKER_OPEN", 15*time.Minute)),
		RateLimitBackoffBase:         env.GetDuration("TRIP_RATE_LIMIT_BACKOFF_BASE", env.GetDuration("RATE_LIMIT_BACKOFF_BASE", 2*time.Minute)),
		RateLimitBackoffMax:          env.GetDuration("TRIP_RATE_LIMIT_BACKOFF_MAX", env.GetDuration("RATE_LIMIT_BACKOFF_MAX", 45*time.Minute)),
		TransientBackoffBase:         env.GetDuration("TRIP_TRANSIENT_BACKOFF_BASE", env.GetDuration("TRANSIENT_BACKOFF_BASE", 30*time.Second)),
		TransientBackoffMax:          env.GetDuration("TRIP_TRANSIENT_BACKOFF_MAX", env.GetDuration("TRANSIENT_BACKOFF_MAX", 10*time.Minute)),
		CollectJobSpacing:            env.GetDuration("TRIP_COLLECT_JOB_SPACING", env.GetDuration("COLLECT_JOB_SPACING", 8*time.Second)),
		BaseURL:                      env.GetString("TRIP_BASE_URL", "https://gateway-v2.trip.ir/api"),
		SiteURL:                      env.GetString("TRIP_SITE_URL", "https://www.trip.ir"),
		Cookie:                       env.GetString("TRIP_COOKIE", ""),
		MetricsAddr:                  env.GetString("TRIP_METRICS_ADDR", env.GetString("METRICS_ADDR", ":9097")),
		Environment:                  env.GetString("APP_ENV", "development"),
		LogLevel:                     env.GetString("LOG_LEVEL", "info"),
	}
}

func mapCabinType(class string) string {
	switch strings.ToLower(strings.TrimSpace(class)) {
	case "business", "c":
		return "BUSINESS"
	case "first", "f":
		return "FIRST"
	case "premium_economy", "premium", "w":
		return "PREMIUMECONOMY"
	default:
		return "ECONOMY"
	}
}
