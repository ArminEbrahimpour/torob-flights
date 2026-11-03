package main

import (
	"context"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/joho/godotenv"
	"github.com/redis/go-redis/v9"

	"github.com/airbar/flight-service/services/api/internal/config"
	"github.com/airbar/flight-service/services/api/internal/flights"
	apihttp "github.com/airbar/flight-service/services/api/internal/http"
	"github.com/airbar/flight-service/services/api/internal/pnr"
	"github.com/airbar/flight-service/services/api/internal/pnr/checkers/mahan"
	"github.com/airbar/flight-service/services/api/internal/pnr/checkers/mahan/upstream"
	"github.com/airbar/flight-service/shared/pkg/logging"
	"github.com/airbar/flight-service/shared/pkg/postgres"
)

func main() {
	_ = godotenv.Load()
	cfg := config.Load()
	logging.Setup(cfg.Environment, cfg.LogLevel)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	pool, err := postgres.Connect(ctx, cfg.DatabaseURL, 16)
	if err != nil {
		slog.Error("database connect failed", "error", err)
		os.Exit(1)
	}
	defer pool.Close()

	postgresFlightsRepo := flights.NewPostgresRepository(pool.Pool)
	flightsRepo := flights.NewHybridRepository(postgresFlightsRepo, cfg.ElasticsearchURL, cfg.ElasticsearchIndex)
	flightsService := flights.NewService(flightsRepo)

	var pnrCache pnr.Cache = pnr.NoopCache{}
	var redisClient *redis.Client
	if cfg.RedisURL != "" {
		opt, err := redis.ParseURL(cfg.RedisURL)
		if err != nil {
			slog.Error("invalid redis url", "error", err)
			os.Exit(1)
		}
		redisClient = redis.NewClient(opt)
		if err := redisClient.Ping(ctx).Err(); err != nil {
			slog.Error("redis connect failed", "error", err)
			os.Exit(1)
		}
		pnrCache = pnr.NewRedisCache(redisClient, cfg.PNRCachePrefix, cfg.PNRCacheTTL)
		slog.Info("pnr cache enabled", "redis", cfg.RedisURL)
	}

	location, err := time.LoadLocation(cfg.AirlineTimezone)
	if err != nil {
		slog.Error("invalid airline timezone", "error", err)
		os.Exit(1)
	}

	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.MaxIdleConns = 50
	transport.MaxIdleConnsPerHost = 10
	mahanClient := upstream.NewClient(upstream.ClientConfig{
		HTTPClient:      &http.Client{Transport: transport, Timeout: cfg.MahanRequestTimeout},
		Endpoint:        cfg.MahanEndpoint,
		Origin:          cfg.MahanOrigin,
		Referer:         cfg.MahanReferer,
		UserAgent:       cfg.MahanUserAgent,
		Culture:         cfg.MahanCulture,
		MaxResponseBody: cfg.MaxResponseBodyBytes,
		RetryCount:      cfg.MahanRetryCount,
	})
	pnrRegistry := pnr.NewRegistry(mahan.NewChecker(mahanClient, location))
	pnrService := pnr.NewService(pnrRegistry, pnrCache)

	router := apihttp.NewRouter(apihttp.Deps{
		Flights:      flightsService,
		PNR:          pnrService,
		MaxBodyBytes: cfg.MaxRequestBodyBytes,
		ReadyCheck: func(r *http.Request) error {
			if err := flightsService.Ping(r.Context()); err != nil {
				return err
			}
			return pnrService.PingCache(r.Context())
		},
	})

	server := &http.Server{
		Addr:              cfg.HTTPAddr,
		Handler:           router,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       cfg.ReadTimeout,
		WriteTimeout:      cfg.WriteTimeout,
		IdleTimeout:       60 * time.Second,
	}

	go func() {
		slog.Info("flight-api listening", "addr", cfg.HTTPAddr)
		if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			slog.Error("flight-api failed", "error", err)
			stop()
		}
	}()

	<-ctx.Done()
	shutdownCtx, cancel := context.WithTimeout(context.Background(), cfg.ShutdownTimeout)
	defer cancel()
	_ = server.Shutdown(shutdownCtx)
	_ = pnrService.Close()
	if redisClient != nil {
		_ = redisClient.Close()
	}
	slog.Info("flight-api stopped")
}
