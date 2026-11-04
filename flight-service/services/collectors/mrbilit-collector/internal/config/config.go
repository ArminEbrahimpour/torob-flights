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
	FlightClass                  string
	CabinType                    string
	HTTPTimeout                  time.Duration
	CircuitBreakerThreshold      int
	CircuitBreakerOpen           time.Duration
	RateLimitBackoffBase         time.Duration
	RateLimitBackoffMax          time.Duration
	TransientBackoffBase         time.Duration
	TransientBackoffMax          time.Duration
	CollectJobSpacing            time.Duration
	MaxSeedBacklog               int
	MetricsAddr                  string
	Environment                  string
	LogLevel                     string
	BaseURL                      string
	Referer                      string
	OriginHeader                 string
	RequestSource                string
	Cookie                       string
}

func Load() Config {
	flightClass := env.GetString("FLIGHT_CLASS", env.GetString("MRBILIT_FLIGHT_CLASS", "economy"))
	return Config{
		DatabaseURL:                  env.GetString("DATABASE_URL", "postgres://flight:flight@127.0.0.1:5433/flight_services?sslmode=disable"),
		MigrationsPath:               env.GetString("MIGRATIONS_PATH", "migrations/001_flights_core.sql"),
		MultiCollectorMigrationsPath: env.GetString("MULTI_COLLECTOR_MIGRATION_PATH", "migrations/002_multi_collector.sql"),
		MaxWorkers:                   env.MaxInt(1, env.GetInt("MRBILIT_MAX_WORKERS", env.GetInt("MAX_WORKERS", 1))),
		RPS:                          env.PositiveFloat(env.GetFloat("MRBILIT_RATE_LIMIT_RPS", env.GetFloat("RATE_LIMIT_RPS", 0.15)), 0.15),
		Burst:                        env.MaxInt(1, env.GetInt("MRBILIT_RATE_LIMIT_BURST", env.GetInt("RATE_LIMIT_BURST", 1))),
		PollInterval:                 env.GetDuration("MRBILIT_POLL_INTERVAL", env.GetDuration("POLL_INTERVAL", time.Second)),
		SeedInterval:                 env.GetDuration("MRBILIT_SEED_INTERVAL", env.GetDuration("SEED_INTERVAL", 2*time.Minute)),
		JobStaleAfter:                env.GetDuration("MRBILIT_JOB_STALE_AFTER", env.GetDuration("JOB_STALE_AFTER", 15*time.Minute)),
		Adult:                        env.MaxInt(1, env.GetInt("MRBILIT_ADULT", env.GetInt("ADULT", 1))),
		FlightClass:                  flightClass,
		CabinType:                    mapCabinType(flightClass),
		HTTPTimeout:                  env.GetDuration("MRBILIT_HTTP_TIMEOUT", env.GetDuration("HTTP_TIMEOUT", 90*time.Second)),
		CircuitBreakerThreshold:      env.MaxInt(1, env.GetInt("MRBILIT_CIRCUIT_BREAKER_THRESHOLD", env.GetInt("CIRCUIT_BREAKER_THRESHOLD", 3))),
		CircuitBreakerOpen:           env.GetDuration("MRBILIT_CIRCUIT_BREAKER_OPEN", env.GetDuration("CIRCUIT_BREAKER_OPEN", 10*time.Minute)),
		RateLimitBackoffBase:         env.GetDuration("MRBILIT_RATE_LIMIT_BACKOFF_BASE", env.GetDuration("RATE_LIMIT_BACKOFF_BASE", time.Minute)),
		RateLimitBackoffMax:          env.GetDuration("MRBILIT_RATE_LIMIT_BACKOFF_MAX", env.GetDuration("RATE_LIMIT_BACKOFF_MAX", 30*time.Minute)),
		TransientBackoffBase:         env.GetDuration("MRBILIT_TRANSIENT_BACKOFF_BASE", env.GetDuration("TRANSIENT_BACKOFF_BASE", 15*time.Second)),
		TransientBackoffMax:          env.GetDuration("MRBILIT_TRANSIENT_BACKOFF_MAX", env.GetDuration("TRANSIENT_BACKOFF_MAX", 5*time.Minute)),
		CollectJobSpacing:            env.GetDuration("MRBILIT_COLLECT_JOB_SPACING", env.GetDuration("COLLECT_JOB_SPACING", 8*time.Second)),
		MaxSeedBacklog:               env.MaxInt(100, env.GetInt("MRBILIT_MAX_SEED_BACKLOG", env.GetInt("MAX_SEED_BACKLOG", 5000))),
		MetricsAddr:                  env.GetString("MRBILIT_METRICS_ADDR", env.GetString("METRICS_ADDR", ":9096")),
		Environment:                  env.GetString("APP_ENV", "development"),
		LogLevel:                     env.GetString("LOG_LEVEL", "info"),
		BaseURL:                      env.GetString("MRBILIT_BASE_URL", "https://flight.atighgasht.com"),
		Referer:                      env.GetString("MRBILIT_REFERER", "https://www.mrbilit.ir"),
		OriginHeader:                 env.GetString("MRBILIT_ORIGIN_HEADER", "https://www.mrbilit.ir"),
		RequestSource:                env.GetString("MRBILIT_REQUEST_SOURCE", "www.mrbilit.ir"),
		Cookie:                       env.GetString("MRBILIT_COOKIE", ""),
	}
}

func mapCabinType(class string) string {
	switch strings.ToLower(strings.TrimSpace(class)) {
	case "business", "c":
		return "C"
	case "first", "f":
		return "F"
	case "premium_economy", "premium", "w":
		return "W"
	default:
		return "Y"
	}
}
