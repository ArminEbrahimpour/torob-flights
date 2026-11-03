package upstream

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

var ErrUpstream = errors.New("mahan upstream error")

type Client struct {
	httpClient      *http.Client
	endpoint        string
	origin          string
	referer         string
	userAgent       string
	culture         string
	maxResponseBody int64
	retryCount      int
}

type ClientConfig struct {
	HTTPClient      *http.Client
	Endpoint        string
	Origin          string
	Referer         string
	UserAgent       string
	Culture         string
	MaxResponseBody int64
	RetryCount      int
}

func NewClient(cfg ClientConfig) *Client {
	return &Client{
		httpClient:      cfg.HTTPClient,
		endpoint:        cfg.Endpoint,
		origin:          cfg.Origin,
		referer:         cfg.Referer,
		userAgent:       cfg.UserAgent,
		culture:         cfg.Culture,
		maxResponseBody: cfg.MaxResponseBody,
		retryCount:      cfg.RetryCount,
	}
}

func (c *Client) Lookup(ctx context.Context, request PNRRequest) (PNRLookupResponse, error) {
	payload, err := json.Marshal(request)
	if err != nil {
		return PNRLookupResponse{}, fmt.Errorf("marshal Mahan request: %w", err)
	}
	var lastError error
	for attempt := 0; attempt <= c.retryCount; attempt++ {
		if attempt > 0 {
			select {
			case <-ctx.Done():
				return PNRLookupResponse{}, ctx.Err()
			case <-time.After(time.Duration(attempt) * 250 * time.Millisecond):
			}
		}
		result, retry, err := c.doLookup(ctx, payload)
		if err == nil {
			return result, nil
		}
		lastError = err
		if !retry {
			break
		}

	}
	return PNRLookupResponse{}, lastError
}

func (c *Client) doLookup(ctx context.Context, payload []byte) (PNRLookupResponse, bool, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.endpoint, bytes.NewReader(payload))
	if err != nil {
		return PNRLookupResponse{}, false, fmt.Errorf("create Mahan request: %w", err)
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", c.userAgent)
	if c.origin != "" {
		req.Header.Set("Origin", c.origin)
	}
	if c.referer != "" {
		req.Header.Set("Referer", c.referer)
	}
	if c.culture != "" {
		req.Header.Set("Accept-Language", c.culture)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return PNRLookupResponse{}, true, fmt.Errorf("%w: request failed: %v", ErrUpstream, err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, c.maxResponseBody+1))
	if err != nil {
		return PNRLookupResponse{}, true, fmt.Errorf("%w: read respnose: %v", ErrUpstream, err)
	}
	if int64(len(body)) > c.maxResponseBody {
		return PNRLookupResponse{}, false, fmt.Errorf("%w: response exceeded size limit", ErrUpstream)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		retry := resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode >= 500
		return PNRLookupResponse{}, retry, fmt.Errorf("%w: HTTP %d", ErrUpstream, resp.StatusCode)
	}

	var result PNRLookupResponse
	if err := json.Unmarshal(body, &result); err != nil {
		preview := strings.TrimSpace(string(body))
		if len(preview) > 160 {
			preview = preview[:160]
		}
		return PNRLookupResponse{}, false, fmt.Errorf("%w: invalid JSON response (%s)", ErrUpstream, preview)
	}
	return result, false, nil
}
