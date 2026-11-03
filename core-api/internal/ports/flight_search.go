package ports

import (
	"context"
	"github.com/torob/core-api/internal/domain"
)

type FlightSearch interface {
	Search(ctx context.Context, query domain.SearchQuery) (domain.SearchResult, error)
	Ready(ctx context.Context) error
}

type FlightRanker interface {
	Ranked(ctx context.Context, query domain.SearchQuery) (domain.RankedResult, error)
}
