package config

import (
	"time"

	"github.com/airbar/flight-service/shared/pkg/env"
)

type Config struct {
	HTTPAddr            string
	DatabaseURL         string
	ElasticsearchURL    string
	ElasticsearchIndex  string
	RedisURL            string
	MaxRequestBodyBytes int64
	ReadTimeout         time.Duration
	WriteTimeout        time.Duration
	ShutdownTimeout     time.Duration
	Environment         string
	LogLevel            string

	MahanEndpoint        string
	MahanOrigin          string
	MahanReferer         string
	MahanUserAgent       string
	MahanCulture         string
	AirlineTimezone      string
	MahanRequestTimeout  time.Duration
	MahanRetryCount      int
	MaxResponseBodyBytes int64
	PNRCacheTTL          time.Duration
	PNRCachePrefix       string
}

func Load() Config {
	return Config{
		HTTPAddr:             env.GetString("HTTP_ADDR", ":8090"),
		DatabaseURL:          env.GetString("DATABASE_URL", "postgres://flight:flight@127.0.0.1:5433/flight_services?sslmode=disable"),
		ElasticsearchURL:     env.GetString("ELASTICSEARCH_URL", ""),
		ElasticsearchIndex:   env.GetString("ELASTICSEARCH_FLIGHTS_INDEX", "flight_offers"),
		RedisURL:             env.GetString("REDIS_URL", "redis://127.0.0.1:6380/5"),
		MaxRequestBodyBytes:  env.GetInt64("MAX_REQUEST_BODY_BYTES", 1<<20),
		ReadTimeout:          env.GetDuration("READ_TIMEOUT", 10*time.Second),
		WriteTimeout:         env.GetDuration("WRITE_TIMEOUT", 30*time.Second),
		ShutdownTimeout:      env.GetDuration("SHUTDOWN_TIMEOUT", 10*time.Second),
		Environment:          env.GetString("APP_ENV", "development"),
		LogLevel:             env.GetString("LOG_LEVEL", "info"),
		MahanEndpoint:        env.GetString("MAHAN_PNR_ENDPOINT", "https://www.mahanair.co.ir/api/Reservation/pnr"),
		MahanOrigin:          env.GetString("MAHAN_ORIGIN", "https://www.mahanair.co.ir"),
		MahanReferer:         env.GetString("MAHAN_REFERER", "https://www.mahanair.co.ir/manage-booking"),
		MahanUserAgent:       env.GetString("MAHAN_USER_AGENT", "AirBar-Flight-Service/1.0"),
		MahanCulture:         env.GetString("MAHAN_CULTURE", "en-US"),
		AirlineTimezone:      env.GetString("AIRLINE_TIMEZONE", "Asia/Tehran"),
		MahanRequestTimeout:  env.GetDuration("MAHAN_REQUEST_TIMEOUT", 12*time.Second),
		MahanRetryCount:      env.GetInt("MAHAN_RETRY_COUNT", 1),
		MaxResponseBodyBytes: env.GetInt64("MAX_RESPONSE_BODY_BYTES", 4<<20),
		PNRCacheTTL:          env.GetDuration("PNR_CACHE_TTL", 10*time.Minute),
		PNRCachePrefix:       env.GetString("PNR_CACHE_PREFIX", "airbar:pnr:v1"),
	}
}
