package httpapi

import (
	"encoding/json"
	"github.com/torob/ranker/internal/domain"
	"github.com/torob/ranker/internal/ports"
	"log/slog"
	"net/http"
	"strings"
	"time"
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
	return requestLog(m)
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
		slog.Info("http request", "method", r.Method, "path", r.URL.Path, "status", sw.status, "bytes", sw.bytes, "duration_ms", time.Since(started).Milliseconds())
	})
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
