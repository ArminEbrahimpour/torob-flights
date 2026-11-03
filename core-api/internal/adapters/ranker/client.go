package ranker

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
	base string
	http *http.Client
}

func New(base string, timeout time.Duration) *Client {
	return &Client{base: strings.TrimRight(strings.TrimSpace(base), "/"), http: &http.Client{Timeout: timeout}}
}
func (c *Client) Ranked(ctx context.Context, q domain.SearchQuery) (domain.RankedResult, error) {
	p := url.Values{"origin": {q.Origin}, "destination": {q.Destination}, "date": {q.Date}, "mode": {q.Mode}, "max_stops": {q.MaxStops}}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.base+"/v1/rankings/flights?"+p.Encode(), nil)
	if err != nil {
		return domain.RankedResult{}, err
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return domain.RankedResult{}, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if err != nil {
		return domain.RankedResult{}, err
	}
	if resp.StatusCode >= 300 {
		return domain.RankedResult{}, fmt.Errorf("ranker returned %s", resp.Status)
	}
	var envelope struct {
		Data []any `json:"data"`
	}
	if err := json.Unmarshal(body, &envelope); err != nil {
		return domain.RankedResult{}, err
	}
	return domain.RankedResult{Items: envelope.Data}, nil
}
