package httpapi

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/torob/core-api/internal/domain"
	"github.com/torob/core-api/internal/ports"
)

type Handler struct {
	flights ports.FlightSearch
	ranker  ports.FlightRanker
}

func New(flights ports.FlightSearch) *Handler { return &Handler{flights: flights} }

func NewWithRanker(flights ports.FlightSearch, ranker ports.FlightRanker) *Handler {
	return &Handler{flights: flights, ranker: ranker}
}

func (h *Handler) Router() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /health/live", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok", "service": "torob-core-api"})
	})
	mux.HandleFunc("GET /health/ready", h.ready)
	mux.HandleFunc("GET /v1/flights", h.searchFlights)
	mux.HandleFunc("GET /v1/search/flights", h.searchFlights)
	mux.HandleFunc("GET /v1/marketplace/search", h.marketplace)
	if h.ranker != nil {
		mux.HandleFunc("GET /v1/flights/ranked", h.ranked)
	}
	return requestLog(requestID(mux))
}

func (h *Handler) marketplace(w http.ResponseWriter, r *http.Request) {
	query := strings.TrimSpace(r.URL.Query().Get("q"))
	if query == "" {
		writeError(w, http.StatusBadRequest, "invalid_request", "q is required")
		return
	}
	result, err := h.flights.Marketplace(r.Context(), query, strings.TrimSpace(r.URL.Query().Get("page")), strings.TrimSpace(r.URL.Query().Get("page_size")), strings.TrimSpace(r.URL.Query().Get("date")))
	if err != nil {
		writeError(w, http.StatusBadGateway, "marketplace_search_failed", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"data": result, "request_id": requestIDValue(r)})
}

func (h *Handler) ready(w http.ResponseWriter, r *http.Request) {
	if err := h.flights.Ready(r.Context()); err != nil {
		writeError(w, http.StatusServiceUnavailable, "flight_service_unavailable", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ready"})
}

func (h *Handler) searchFlights(w http.ResponseWriter, r *http.Request) {
	q := domain.SearchQuery{}
	for key, dst := range map[string]*string{"origin": &q.Origin, "destination": &q.Destination, "date": &q.Date, "source": &q.Source, "airline": &q.Airline, "flight_number": &q.FlightNumber, "min_price_toman": &q.MinPriceToman, "max_price_toman": &q.MaxPriceToman, "charter": &q.Charter, "max_stops": &q.MaxStops, "page": &q.Page, "page_size": &q.PageSize} {
		*dst = strings.TrimSpace(r.URL.Query().Get(key))
	}
	if q.Origin == "" || q.Destination == "" || q.Date == "" {
		writeError(w, http.StatusBadRequest, "invalid_request", "origin, destination, and date are required")
		return
	}
	result, err := h.flights.Search(r.Context(), q)
	if err != nil {
		writeError(w, http.StatusBadGateway, "flight_search_failed", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"data": result, "request_id": requestIDValue(r)})
}

func (h *Handler) ranked(w http.ResponseWriter, r *http.Request) {
	q := domain.SearchQuery{Origin: strings.TrimSpace(r.URL.Query().Get("origin")), Destination: strings.TrimSpace(r.URL.Query().Get("destination")), Date: strings.TrimSpace(r.URL.Query().Get("date")), Mode: strings.TrimSpace(r.URL.Query().Get("mode")), MaxStops: strings.TrimSpace(r.URL.Query().Get("max_stops")), WeightPrice: strings.TrimSpace(r.URL.Query().Get("weight_price")), WeightDuration: strings.TrimSpace(r.URL.Query().Get("weight_duration")), WeightStops: strings.TrimSpace(r.URL.Query().Get("weight_stops")), WeightBaggage: strings.TrimSpace(r.URL.Query().Get("weight_baggage")), WeightDepart: strings.TrimSpace(r.URL.Query().Get("weight_depart")), DepartMinute: strings.TrimSpace(r.URL.Query().Get("depart_minute"))}
	if q.Origin == "" || q.Destination == "" || q.Date == "" {
		writeError(w, http.StatusBadRequest, "invalid_request", "origin, destination, and date are required")
		return
	}
	result, err := h.ranker.Ranked(r.Context(), q)
	if err == nil && len(result.Items) > 0 {
		writeJSON(w, http.StatusOK, map[string]any{"data": result.Items, "request_id": requestIDValue(r)})
		return
	}
	// Ranker only has flights the consumer has ingested. A dated search still
	// has to return the offers already stored by the collectors.
	q.PageSize = "30"
	searched, searchErr := h.flights.Search(r.Context(), q)
	if searchErr != nil {
		if err != nil {
			writeError(w, http.StatusBadGateway, "ranking_failed", err.Error())
			return
		}
		writeError(w, http.StatusBadGateway, "flight_search_failed", searchErr.Error())
		return
	}
	items := make([]any, 0, len(searched.Offers))
	for _, offer := range searched.Offers {
		items = append(items, map[string]any{"flight": offer})
	}
	writeJSON(w, http.StatusOK, map[string]any{"data": items, "request_id": requestIDValue(r)})
}

func requestID(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := strings.TrimSpace(r.Header.Get("X-Request-ID"))
		if id == "" {
			id = time.Now().UTC().Format("20060102T150405.000000000")
		}
		w.Header().Set("X-Request-ID", id)
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), requestIDKey{}, id)))
	})
}

type statusWriter struct {
	http.ResponseWriter
	status, bytes int
}

func (w *statusWriter) WriteHeader(status int) {
	w.status = status
	w.ResponseWriter.WriteHeader(status)
}
func (w *statusWriter) Write(p []byte) (int, error) {
	if w.status == 0 {
		w.status = http.StatusOK
	}
	n, err := w.ResponseWriter.Write(p)
	w.bytes += n
	return n, err
}

func requestLog(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		started := time.Now()
		sw := &statusWriter{ResponseWriter: w}
		next.ServeHTTP(sw, r)
		slog.Info("http request", "method", r.Method, "path", r.URL.Path, "status", sw.status, "bytes", sw.bytes, "duration_ms", time.Since(started).Milliseconds(), "request_id", requestIDValue(r))
	})
}

type requestIDKey struct{}

func requestIDValue(r *http.Request) string {
	if value, ok := r.Context().Value(requestIDKey{}).(string); ok {
		return value
	}
	return ""
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}
func writeError(w http.ResponseWriter, status int, code, message string) {
	writeJSON(w, status, map[string]any{"error": map[string]string{"code": code, "message": message}})
}
