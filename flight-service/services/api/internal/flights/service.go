package flights

import (
	"context"

	"github.com/airbar/flight-service/shared/domain/flight"
	"github.com/airbar/flight-service/shared/pkg/offerurl"
)

type Service struct {
	repo Repository
}

func NewService(repo Repository) *Service {
	return &Service{repo: repo}
}

func (s *Service) Search(ctx context.Context, q flight.SearchQuery) (flight.SearchResult, error) {
	result, err := s.repo.Search(ctx, q)
	if err != nil {
		return result, err
	}
	for i := range result.Offers {
		result.Offers[i].OfferURL = offerurl.ForFlight(result.Offers[i].Source, result.Offers[i])
	}
	return result, nil
}

func (s *Service) Validate(ctx context.Context, req flight.ValidateRequest) (flight.ValidateResponse, error) {
	return s.repo.Validate(ctx, req)
}

func (s *Service) ListSources(ctx context.Context) ([]string, error) {
	return s.repo.ListSources(ctx)
}

func (s *Service) AddRoute(ctx context.Context, origin, destination string, priority, refreshMinutes, windowDays int) error {
	return s.repo.AddRoute(ctx, origin, destination, priority, refreshMinutes, windowDays)
}

func (s *Service) Ping(ctx context.Context) error {
	return s.repo.Ping(ctx)
}

func (s *Service) ListUniqueFutureFlights(ctx context.Context, q flight.UniqueFutureFlightsQuery) (flight.UniqueFutureFlightsListResult, error) {
	return s.repo.ListUniqueFutureFlights(ctx, q)
}

func (s *Service) ValidateByDate(ctx context.Context, q flight.FlightByDateQuery) (flight.FlightByDateResult, error) {
	return s.repo.GetFlightByDate(ctx, q)
}

type AddRouteRequest struct {
	Origin         string `json:"origin"`
	Destination    string `json:"destination"`
	Priority       int    `json:"priority"`
	RefreshMinutes int    `json:"refresh_minutes"`
	WindowDays     int    `json:"window_days"`
}
