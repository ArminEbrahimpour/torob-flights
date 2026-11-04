package config

import (
	"os"
	"time"
)

type Config struct {
	HTTPAddr, FlightServiceURL, RankerURL string
	RequestTimeout                        time.Duration
}

func Load() Config {
	return Config{HTTPAddr: get("HTTP_ADDR", ":8080"), FlightServiceURL: get("FLIGHT_SERVICE_URL", "http://localhost:8090"), RankerURL: get("RANKER_URL", "http://localhost:8081"), RequestTimeout: duration("REQUEST_TIMEOUT", 15*time.Second)}
}
func get(key, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
}
func duration(key string, fallback time.Duration) time.Duration {
	value := os.Getenv(key)
	if value == "" {
		return fallback
	}
	parsed, err := time.ParseDuration(value)
	if err != nil {
		return fallback
	}
	return parsed
}
