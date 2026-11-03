package main

import (
	"context"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/torob/core-api/internal/adapters/flightservice"
	httpadapter "github.com/torob/core-api/internal/adapters/httpapi"
	"github.com/torob/core-api/internal/adapters/ranker"
	"github.com/torob/core-api/internal/config"
)

func main() {
	cfg := config.Load()
	flightClient := flightservice.New(cfg.FlightServiceURL, cfg.RequestTimeout)
	rankerClient := ranker.New(cfg.RankerURL, cfg.RequestTimeout)
	server := &http.Server{Addr: cfg.HTTPAddr, Handler: httpadapter.NewWithRanker(flightClient, rankerClient).Router(), ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 10 * time.Second, WriteTimeout: 30 * time.Second, IdleTimeout: 60 * time.Second}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	go func() {
		slog.Info("torob-core-api listening", "addr", cfg.HTTPAddr, "flight_service", cfg.FlightServiceURL)
		if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			slog.Error("server failed", "error", err)
			stop()
		}
	}()
	<-ctx.Done()
	shutdown, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	_ = server.Shutdown(shutdown)
}
