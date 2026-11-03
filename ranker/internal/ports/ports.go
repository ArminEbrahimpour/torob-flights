package ports

import (
	"context"
	"github.com/torob/ranker/internal/domain"
)

type FlightStore interface {
	Upsert(ctx context.Context, flight domain.Flight) error
	Rebuild(ctx context.Context) error
	Search(ctx context.Context, query domain.SearchQuery) ([]domain.RankedFlight, error)
	Ping(ctx context.Context) error
}
type EventConsumer interface {
	Consume(ctx context.Context, handler func(context.Context, domain.Flight) error) error
}
