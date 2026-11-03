package httpapi

import (
	"encoding/json"
	"github.com/torob/ranker/internal/domain"
	"github.com/torob/ranker/internal/ports"
	"net/http"
	"strings"
)

type Handler struct{ store ports.FlightStore }

func New(store ports.FlightStore) *Handler { return &Handler{store: store} }
func (h *Handler) Router() http.Handler {
	m := http.NewServeMux()
	m.HandleFunc("GET /health/live", func(w http.ResponseWriter, _ *http.Request) {
		write(w, 200, map[string]string{"status": "ok", "service": "torob-ranker"})
	})
	m.HandleFunc("GET /health/ready", h.ready)
	m.HandleFunc("GET /v1/rankings/flights", h.search)
	return m
}
func (h *Handler) ready(w http.ResponseWriter, r *http.Request) {
	if err := h.store.Ping(r.Context()); err != nil {
		write(w, 503, map[string]any{"error": map[string]string{"code": "ranker_unavailable", "message": err.Error()}})
		return
	}
	write(w, 200, map[string]string{"status": "ready"})
}
func (h *Handler) search(w http.ResponseWriter, r *http.Request) {
	q := domain.SearchQuery{Origin: strings.ToUpper(strings.TrimSpace(r.URL.Query().Get("origin"))), Destination: strings.ToUpper(strings.TrimSpace(r.URL.Query().Get("destination"))), Date: strings.TrimSpace(r.URL.Query().Get("date")), Mode: strings.ToLower(strings.TrimSpace(r.URL.Query().Get("mode"))), MaxStops: strings.TrimSpace(r.URL.Query().Get("max_stops"))}
	items, err := h.store.Search(r.Context(), q)
	if err != nil {
		write(w, 502, map[string]any{"error": map[string]string{"code": "ranking_failed", "message": err.Error()}})
		return
	}
	write(w, 200, map[string]any{"data": items})
}
func write(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
