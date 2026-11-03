package flights

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
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
	if r.esURL == "" {
		return r.pg.Search(ctx, q)
	}
	result, err := r.searchElastic(ctx, q)
	if err != nil {
		// Search remains available while the read model catches up or is down.
		return r.pg.Search(ctx, q)
	}
	if result.Total == 0 && q.Origin != "" && q.Destination != "" && q.Date != "" {
		result.RefreshRequested = r.pg.enqueueRefresh(ctx, q.Origin, q.Destination, q.Date)
	}
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
		filters = append(filters, map[string]any{"bool": map[string]any{"should": []any{map[string]any{"term": map[string]any{"origin": strings.ToUpper(q.Origin)}}, map[string]any{"term": map[string]any{"origin_query": strings.ToUpper(q.Origin)}}}, "minimum_should_match": 1}})
	}
	if q.Destination != "" {
		filters = append(filters, map[string]any{"bool": map[string]any{"should": []any{map[string]any{"term": map[string]any{"destination": strings.ToUpper(q.Destination)}}, map[string]any{"term": map[string]any{"destination_query": strings.ToUpper(q.Destination)}}}, "minimum_should_match": 1}})
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
