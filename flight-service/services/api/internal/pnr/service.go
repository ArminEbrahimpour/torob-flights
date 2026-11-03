package pnr

import (
	"context"
	"errors"
	"log/slog"
	"strings"
	"time"
)

type Service struct {
	registry *Registry
	cache    Cache
}

func NewService(registry *Registry, cache Cache) *Service {
	return &Service{registry: registry, cache: cache}
}

func (s *Service) Airlines() []string {
	return s.registry.Airlines()
}

func (s *Service) Verify(ctx context.Context, req VerifyRequest) (CheckResult, error) {
	req.Airline = normalizeAirline(req.Airline)
	req.PNR = strings.ToUpper(strings.TrimSpace(req.PNR))
	req.LastName = strings.TrimSpace(req.LastName)
	req.FlightDate = strings.TrimSpace(req.FlightDate)

	cacheKey := CacheKey{
		Airline:    req.Airline,
		PNR:        req.PNR,
		LastName:   req.LastName,
		FlightDate: req.FlightDate,
	}
	if cached, err := s.cache.Get(ctx, cacheKey); err == nil && cached != nil {
		cached.Source = "cache"
		cached.CheckedAt = time.Now().UTC()
		return *cached, nil
	} else if err != nil {
		slog.WarnContext(ctx, "pnr cache read failed", "error", err)
	}

	result, err := s.registry.Check(ctx, req.Airline, req)
	if err != nil {
		return CheckResult{}, err
	}
	result.Airline = req.Airline

	if err := s.cache.Set(ctx, cacheKey, result); err != nil {
		slog.WarnContext(ctx, "pnr cache write failed", "error", err)
	}
	return result, nil
}

func (s *Service) PingCache(ctx context.Context) error {
	return s.cache.Ping(ctx)
}

func (s *Service) Close() error {
	return s.cache.Close()
}

func IsUnsupportedAirline(err error) bool {
	return errors.Is(err, ErrUnsupportedAirline)
}
