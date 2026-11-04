package flights

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/airbar/flight-service/shared/domain/flight"
)

// HybridRepository keeps PostgreSQL authoritative while using Elasticsearch
// as an optional, eventually consistent search read model.
type HybridRepository struct {
	pg    *PostgresRepository
	esURL string
	index string
	http  *http.Client
}

func NewHybridRepository(pg *PostgresRepository, esURL, index string) *HybridRepository {
	return &HybridRepository{pg: pg, esURL: strings.TrimRight(strings.TrimSpace(esURL), "/"), index: index, http: &http.Client{Timeout: 5 * time.Second}}
}

func (r *HybridRepository) Search(ctx context.Context, q flight.SearchQuery) (flight.SearchResult, error) {
	started := time.Now()
	slog.InfoContext(ctx, "flight search started", "origin", q.Origin, "destination", q.Destination, "date", q.Date, "airline", q.Airline, "page", q.Page)
	if r.esURL == "" {
		result, err := r.pg.Search(ctx, q)
		slog.InfoContext(ctx, "flight search completed", "backend", "postgres", "total", result.Total, "duration_ms", time.Since(started).Milliseconds(), "error", err)
		return result, err
	}
	result, err := r.searchElastic(ctx, q)
	if err != nil || result.Total == 0 {
		// An empty or unavailable index must not hide rows that PostgreSQL has.
		pgResult, pgErr := r.pg.Search(ctx, q)
		if err != nil {
			slog.WarnContext(ctx, "elasticsearch search failed; falling back to postgres", "error", err, "duration_ms", time.Since(started).Milliseconds())
		}
		if pgErr != nil {
			if err != nil {
				slog.InfoContext(ctx, "flight search completed", "backend", "postgres_fallback", "duration_ms", time.Since(started).Milliseconds(), "error", pgErr)
				return flight.SearchResult{}, pgErr
			}
		} else if err != nil || pgResult.Total > 0 {
			slog.InfoContext(ctx, "flight search completed", "backend", "postgres_fallback", "total", pgResult.Total, "duration_ms", time.Since(started).Milliseconds())
			return pgResult, nil
		} else {
			result = pgResult
		}
	}
	if result.Total == 0 && q.Origin != "" && q.Destination != "" && q.Date != "" {
		result.RefreshRequested = r.pg.enqueueRefresh(ctx, q.Origin, q.Destination, q.Date)
	}
	slog.InfoContext(ctx, "flight search completed", "backend", "elasticsearch", "total", result.Total, "duration_ms", time.Since(started).Milliseconds())
	return result, nil
}

func (r *HybridRepository) Validate(ctx context.Context, q flight.ValidateRequest) (flight.ValidateResponse, error) {
	return r.pg.Validate(ctx, q)
}
func (r *HybridRepository) ListUniqueFutureFlights(ctx context.Context, q flight.UniqueFutureFlightsQuery) (flight.UniqueFutureFlightsListResult, error) {
	return r.pg.ListUniqueFutureFlights(ctx, q)
}
func (r *HybridRepository) GetFlightByDate(ctx context.Context, q flight.FlightByDateQuery) (flight.FlightByDateResult, error) {
	return r.pg.GetFlightByDate(ctx, q)
}
func (r *HybridRepository) ListSources(ctx context.Context) ([]string, error) {
	return r.pg.ListSources(ctx)
}
func (r *HybridRepository) AddRoute(ctx context.Context, o, d string, p, rm, wd int) error {
	return r.pg.AddRoute(ctx, o, d, p, rm, wd)
}
func (r *HybridRepository) Ping(ctx context.Context) error { return r.pg.Ping(ctx) }

func (r *HybridRepository) searchElastic(ctx context.Context, q flight.SearchQuery) (flight.SearchResult, error) {
	if q.Page <= 0 {
		q.Page = 1
	}
	if q.PageSize <= 0 {
		q.PageSize = 50
	}
	if q.PageSize > 200 {
		q.PageSize = 200
	}
	filters := make([]any, 0, 8)
	maxAge := freshnessWindow(q.Date, time.Now().UTC())
	filters = append(filters, map[string]any{"range": map[string]any{"collected_at": map[string]any{"gte": time.Now().UTC().Add(-maxAge).Format(time.RFC3339)}}})
	if q.Origin != "" {
		codes := r.pg.searchCodes(ctx, q.Origin)
		filters = append(filters, map[string]any{"bool": map[string]any{"should": []any{map[string]any{"terms": map[string]any{"origin": codes}}, map[string]any{"terms": map[string]any{"origin_query": codes}}}, "minimum_should_match": 1}})
	}
	if q.Destination != "" {
		codes := r.pg.searchCodes(ctx, q.Destination)
		filters = append(filters, map[string]any{"bool": map[string]any{"should": []any{map[string]any{"terms": map[string]any{"destination": codes}}, map[string]any{"terms": map[string]any{"destination_query": codes}}}, "minimum_should_match": 1}})
	}
	if q.Date != "" {
		filters = append(filters, map[string]any{"range": map[string]any{"flight_date": map[string]any{"gte": q.Date, "lt": q.Date + "||+1d"}}})
	}
	if q.Source != "" && q.Source != "all" {
		filters = append(filters, map[string]any{"term": map[string]any{"source": q.Source}})
	}
	if q.Airline != "" {
		filters = append(filters, map[string]any{"term": map[string]any{"airline_code": strings.ToUpper(q.Airline)}})
	}
	if q.FlightNumber != "" {
		filters = append(filters, map[string]any{"term": map[string]any{"flight_number_norm": strings.ToUpper(strings.TrimSpace(q.FlightNumber))}})
	}
	if q.MinPriceToman != nil || q.MaxPriceToman != nil {
		rg := map[string]any{}
		if q.MinPriceToman != nil {
			rg["gte"] = *q.MinPriceToman
		}
		if q.MaxPriceToman != nil {
			rg["lte"] = *q.MaxPriceToman
		}
		filters = append(filters, map[string]any{"range": map[string]any{"price_toman": rg}})
	}
	if q.Charter != nil {
		filters = append(filters, map[string]any{"term": map[string]any{"is_charter": *q.Charter}})
	}
	if q.MaxStops != nil {
		filters = append(filters, map[string]any{"range": map[string]any{"stops": map[string]any{"lte": *q.MaxStops}}})
	}
	body := map[string]any{"from": (q.Page - 1) * q.PageSize, "size": q.PageSize, "query": map[string]any{"bool": map[string]any{"filter": filters}}, "sort": []any{map[string]any{"depart_at": "asc"}}}
	b, _ := json.Marshal(body)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, r.esURL+"/"+url.PathEscape(r.index)+"/_search", bytes.NewReader(b))
	if err != nil {
		return flight.SearchResult{}, err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := r.http.Do(req)
	if err != nil {
		return flight.SearchResult{}, err
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return flight.SearchResult{}, err
	}
	if resp.StatusCode >= 300 {
		return flight.SearchResult{}, fmt.Errorf("elasticsearch search status %d: %s", resp.StatusCode, string(data))
	}
	var raw struct {
		Hits struct {
			Total json.RawMessage `json:"total"`
			Hits  []struct {
				Source flight.Flight `json:"_source"`
			} `json:"hits"`
		} `json:"hits"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return flight.SearchResult{}, err
	}
	total := parseESTotal(raw.Hits.Total)
	offers := make([]flight.Flight, 0, len(raw.Hits.Hits))
	for _, hit := range raw.Hits.Hits {
		offers = append(offers, hit.Source)
	}
	return flight.SearchResult{Origin: q.Origin, Destination: q.Destination, FlightDate: q.Date, Source: q.Source, Total: total, Page: q.Page, PageSize: q.PageSize, Offers: offers, FreshnessMaxAgeSeconds: int(maxAge / time.Second), FreshnessAsOf: time.Now().UTC()}, nil
}

func parseESTotal(raw json.RawMessage) int {
	var n int
	if json.Unmarshal(raw, &n) == nil {
		return n
	}
	var obj struct {
		Value int `json:"value"`
	}
	_ = json.Unmarshal(raw, &obj)
	return obj.Value
}

var _ Repository = (*HybridRepository)(nil)
