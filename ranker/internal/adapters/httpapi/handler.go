package httpapi

import (
	"encoding/json"
	"github.com/torob/ranker/internal/domain"
	"github.com/torob/ranker/internal/ports"
	"github.com/torob/ranker/internal/score"
	"log/slog"
	"net/http"
	"strconv"
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
	q.WeightPrice, q.CustomWeights = weightParam(r, "weight_price", q.CustomWeights)
	q.WeightDuration, q.CustomWeights = weightParam(r, "weight_duration", q.CustomWeights)
	q.WeightStops, q.CustomWeights = weightParam(r, "weight_stops", q.CustomWeights)
	q.WeightBaggage, q.CustomWeights = weightParam(r, "weight_baggage", q.CustomWeights)
	q.WeightDepart, q.CustomWeights = weightParam(r, "weight_depart", q.CustomWeights)
	if minute, ok := intParam(r, "depart_minute"); ok {
		q.PreferDepart = true
		q.DepartMinute = minute
		if strings.TrimSpace(r.URL.Query().Get("weight_depart")) == "" {
			if !q.CustomWeights {
				base := score.ForMode(q.Mode)
				q.WeightPrice, q.WeightDuration, q.WeightStops, q.WeightBaggage = base.Price, base.Duration, base.Stops, base.Baggage
			}
			q.WeightDepart = 0.35
			q.CustomWeights = true
		}
	}
	items, err := h.store.Search(r.Context(), q)
	if err != nil {
		write(w, 502, map[string]any{"error": map[string]string{"code": "ranking_failed", "message": err.Error()}})
		return
	}
	write(w, 200, map[string]any{"data": items})
}
func weightParam(r *http.Request, name string, already bool) (float64, bool) {
	raw := strings.TrimSpace(r.URL.Query().Get(name))
	if raw == "" {
		return 0, already
	}
	n, err := strconv.ParseFloat(raw, 64)
	if err != nil || n < 0 {
		return 0, already
	}
	return n, true
}

func intParam(r *http.Request, name string) (int, bool) {
	raw := strings.TrimSpace(r.URL.Query().Get(name))
	if raw == "" {
		return 0, false
	}
	n, err := strconv.Atoi(raw)
	if err != nil {
		return 0, false
	}
	return n, true
}

func write(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
