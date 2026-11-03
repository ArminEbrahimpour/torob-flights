package flightservice

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/torob/core-api/internal/domain"
)

type Client struct {
	baseURL string
	http    *http.Client
}

func New(baseURL string, timeout time.Duration) *Client {
	return &Client{baseURL: strings.TrimRight(strings.TrimSpace(baseURL), "/"), http: &http.Client{Timeout: timeout}}
}

func (c *Client) Search(ctx context.Context, q domain.SearchQuery) (domain.SearchResult, error) {
	params := url.Values{}
	params.Set("origin", strings.ToUpper(strings.TrimSpace(q.Origin)))
	params.Set("destination", strings.ToUpper(strings.TrimSpace(q.Destination)))
	for key, value := range map[string]string{"date": q.Date, "source": q.Source, "airline": q.Airline, "flight_number": q.FlightNumber, "min_price_toman": q.MinPriceToman, "max_price_toman": q.MaxPriceToman, "charter": q.Charter, "max_stops": q.MaxStops, "page": q.Page, "page_size": q.PageSize} {
		if strings.TrimSpace(value) != "" {
			params.Set(key, value)
		}
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+"/v1/flights?"+params.Encode(), nil)
	if err != nil {
		return domain.SearchResult{}, err
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return domain.SearchResult{}, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if err != nil {
		return domain.SearchResult{}, err
	}
	if resp.StatusCode >= 300 {
		return domain.SearchResult{}, fmt.Errorf("flight-service returned %s", resp.Status)
	}
	var envelope struct {
		Data domain.SearchResult `json:"data"`
	}
	if err := json.Unmarshal(body, &envelope); err != nil {
		return domain.SearchResult{}, fmt.Errorf("decode flight-service response: %w", err)
	}
	return envelope.Data, nil
}

func (c *Client) Ready(ctx context.Context) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+"/health/ready", nil)
	if err != nil {
		return err
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		return fmt.Errorf("flight-service readiness returned %s", resp.Status)
	}
	return nil
}
