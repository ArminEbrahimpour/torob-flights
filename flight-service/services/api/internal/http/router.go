package http

import (
	"net/http"

	"github.com/airbar/flight-service/services/api/internal/flights"
	flightshandler "github.com/airbar/flight-service/services/api/internal/http/flights"
	"github.com/airbar/flight-service/services/api/internal/http/middleware"
	pnrhandler "github.com/airbar/flight-service/services/api/internal/http/pnr"
	ticketshandler "github.com/airbar/flight-service/services/api/internal/http/tickets"
	"github.com/airbar/flight-service/services/api/internal/pnr"
)

type Deps struct {
	Flights      *flights.Service
	PNR          *pnr.Service
	MaxBodyBytes int64
	ReadyCheck   func(r *http.Request) error
}

func NewRouter(deps Deps) http.Handler {
	flightsH := flightshandler.NewHandler(deps.Flights, deps.MaxBodyBytes)
	pnrH := pnrhandler.NewHandler(deps.PNR, deps.MaxBodyBytes)
	ticketsH := ticketshandler.NewHandler()

	mux := http.NewServeMux()
	mux.HandleFunc("GET /health/live", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"status":"ok","service":"flight-services-api"}`))
	})
	mux.HandleFunc("GET /health/ready", func(w http.ResponseWriter, r *http.Request) {
		if deps.ReadyCheck != nil {
			if err := deps.ReadyCheck(r); err != nil {
				http.Error(w, "dependencies unavailable", http.StatusServiceUnavailable)
				return
			}
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"status":"ready"}`))
	})

	mux.HandleFunc("GET /v1/flights", flightsH.Search)
	mux.HandleFunc("POST /v1/flights/validate", flightsH.Validate)
	mux.HandleFunc("GET /v1/flights/sources", flightsH.Sources)
	mux.HandleFunc("POST /v1/routes", flightsH.AddRoute)
	mux.HandleFunc("GET /v1/flights/uniq", flightsH.UniqFlightsPerFlightNumNorm)
	mux.HandleFunc("GET /v1/flights/by-date", flightsH.ValidateByDate)

	mux.HandleFunc("GET /v1/pnr/airlines", pnrH.Airlines)
	mux.HandleFunc("POST /v1/pnr/verify", pnrH.Verify)

	mux.HandleFunc("GET /v1/tickets/", ticketsH.NotImplemented)
	mux.HandleFunc("POST /v1/tickets/", ticketsH.NotImplemented)

	var handler http.Handler = mux
	handler = middleware.SecurityHeadersMiddleware(handler)
	handler = middleware.LoggingMiddleware(handler)
	handler = middleware.RequestIDMiddleware(handler)
	handler = middleware.RecoverMiddleware(handler)
	return handler
}
