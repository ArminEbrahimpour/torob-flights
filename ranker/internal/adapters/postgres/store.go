package postgres

import (
	"context"
	"fmt"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/torob/ranker/internal/airportcodes"
	"github.com/torob/ranker/internal/domain"
	"github.com/torob/ranker/internal/score"
	"sort"
	"strings"
	"time"
)

type Store struct{ pool *pgxpool.Pool }

func New(pool *pgxpool.Pool) *Store { return &Store{pool: pool} }
func (s *Store) Upsert(ctx context.Context, f domain.Flight) error {
	_, err := s.pool.Exec(ctx, `INSERT INTO flights (id,origin,destination,flight_date,airline_code,airline_name,flight_number,depart_at,arrive_at,depart_minute,duration_min,baggage_label,baggage_kg,price_rial,price_toman,is_charter,stops,source,collected_at,offer_url,updated_at) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18,$19,$20,now()) ON CONFLICT (id) DO UPDATE SET origin=EXCLUDED.origin,destination=EXCLUDED.destination,flight_date=EXCLUDED.flight_date,airline_code=EXCLUDED.airline_code,airline_name=EXCLUDED.airline_name,flight_number=EXCLUDED.flight_number,depart_at=EXCLUDED.depart_at,arrive_at=EXCLUDED.arrive_at,depart_minute=EXCLUDED.depart_minute,duration_min=EXCLUDED.duration_min,baggage_label=EXCLUDED.baggage_label,baggage_kg=EXCLUDED.baggage_kg,price_rial=EXCLUDED.price_rial,price_toman=EXCLUDED.price_toman,is_charter=EXCLUDED.is_charter,stops=EXCLUDED.stops,source=EXCLUDED.source,collected_at=EXCLUDED.collected_at,offer_url=EXCLUDED.offer_url,updated_at=now()`, f.ID, f.Origin, f.Destination, f.FlightDate, f.AirlineCode, f.AirlineName, f.FlightNumber, f.DepartAt, f.ArriveAt, f.DepartMinute, f.DurationMin, f.BaggageLabel, f.BaggageKG, f.PriceRial, f.PriceToman, f.IsCharter, f.Stops, f.Source, f.CollectedAt, f.OfferURL)
	return err
}
func (s *Store) Rebuild(ctx context.Context) error {
	rows, err := s.pool.Query(ctx, `SELECT id,origin,destination,flight_date,airline_code,airline_name,flight_number,depart_at,arrive_at,depart_minute,duration_min,baggage_label,baggage_kg,price_rial,price_toman,is_charter,stops,source,collected_at FROM flights`)
	if err != nil {
		return err
	}
	defer rows.Close()
	groups := map[string][]domain.Flight{}
	for rows.Next() {
		var f domain.Flight
		if err := rows.Scan(&f.ID, &f.Origin, &f.Destination, &f.FlightDate, &f.AirlineCode, &f.AirlineName, &f.FlightNumber, &f.DepartAt, &f.ArriveAt, &f.DepartMinute, &f.DurationMin, &f.BaggageLabel, &f.BaggageKG, &f.PriceRial, &f.PriceToman, &f.IsCharter, &f.Stops, &f.Source, &f.CollectedAt); err != nil {
			return err
		}
		groups[f.Origin+"\x00"+f.Destination+"\x00"+f.FlightDate.Format("2006-01-02")] = append(groups[f.Origin+"\x00"+f.Destination+"\x00"+f.FlightDate.Format("2006-01-02")], f)
	}
	if err := rows.Err(); err != nil {
		return err
	}
	for _, flights := range groups {
		ranked := rankGroup(flights)
		tx, err := s.pool.Begin(ctx)
		if err != nil {
			return err
		}
		for rank, row := range ranked {
			if _, err = tx.Exec(ctx, `INSERT INTO ranked_flights (flight_id,origin,destination,flight_date,rank,score,price_score,duration_score,stops_score,baggage_score,ranked_at) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,now()) ON CONFLICT (flight_id) DO UPDATE SET origin=EXCLUDED.origin,destination=EXCLUDED.destination,flight_date=EXCLUDED.flight_date,rank=EXCLUDED.rank,score=EXCLUDED.score,price_score=EXCLUDED.price_score,duration_score=EXCLUDED.duration_score,stops_score=EXCLUDED.stops_score,baggage_score=EXCLUDED.baggage_score,ranked_at=now()`, row.flight.ID, row.flight.Origin, row.flight.Destination, row.flight.FlightDate, rank+1, row.total, row.parts.Price, row.parts.Duration, row.parts.Stops, row.parts.Baggage); err != nil {
				_ = tx.Rollback(ctx)
				return err
			}
		}
		if _, err = tx.Exec(ctx, `DELETE FROM ranked_flights r WHERE r.origin=$1 AND r.destination=$2 AND r.flight_date=$3 AND NOT EXISTS (SELECT 1 FROM flights f WHERE f.id=r.flight_id)`, flights[0].Origin, flights[0].Destination, flights[0].FlightDate); err != nil {
			_ = tx.Rollback(ctx)
			return err
		}
		if err = tx.Commit(ctx); err != nil {
			return err
		}
	}
	return nil
}

type rankedFlight struct {
	flight domain.Flight
	parts  score.Components
	total  float64
}

func rankGroup(flights []domain.Flight) []rankedFlight {
	offers := make([]score.Offer, len(flights))
	for i, f := range flights {
		baggage := -1
		if f.BaggageKG != nil {
			baggage = *f.BaggageKG
		}
		duration := 0
		if f.DurationMin != nil {
			duration = *f.DurationMin
		}
		offers[i] = score.Offer{PriceToman: f.PriceToman, DurationMin: duration, Stops: f.Stops, BaggageKG: baggage}
	}
	parts := score.Group(offers)
	weights := score.BestValue()
	ranked := make([]rankedFlight, len(flights))
	for i, f := range flights {
		ranked[i] = rankedFlight{flight: f, parts: parts[i], total: weights.Total(parts[i], 0)}
	}
	sort.SliceStable(ranked, func(i, j int) bool {
		if ranked[i].total != ranked[j].total {
			return ranked[i].total > ranked[j].total
		}
		return less(ranked[i].flight, ranked[j].flight)
	})
	return ranked
}

func less(a, b domain.Flight) bool {
	// Unknown/zero prices are always last; a malformed offer must not win.
	if (a.PriceToman <= 0) != (b.PriceToman <= 0) {
		return b.PriceToman <= 0
	}
	if a.PriceToman != b.PriceToman {
		return a.PriceToman < b.PriceToman
	}
	if a.Stops != b.Stops {
		return a.Stops < b.Stops
	}
	ad, bd := 1<<30, 1<<30
	if a.DurationMin != nil && *a.DurationMin > 0 {
		ad = *a.DurationMin
	}
	if b.DurationMin != nil && *b.DurationMin > 0 {
		bd = *b.DurationMin
	}
	if ad != bd {
		return ad < bd
	}
	ak, bk := 0, 0
	if a.BaggageKG != nil {
		ak = *a.BaggageKG
	}
	if b.BaggageKG != nil {
		bk = *b.BaggageKG
	}
	if ak != bk {
		return ak > bk
	}
	if a.DepartMinute != b.DepartMinute {
		return a.DepartMinute < b.DepartMinute
	}
	if !a.ArriveAt.Equal(b.ArriveAt) {
		return a.ArriveAt.Before(b.ArriveAt)
	}
	if a.IsCharter != b.IsCharter {
		return !a.IsCharter
	}
	if !a.CollectedAt.Equal(b.CollectedAt) {
		return a.CollectedAt.After(b.CollectedAt)
	}
	return a.AirlineCode < b.AirlineCode
}
func (s *Store) Search(ctx context.Context, q domain.SearchQuery) ([]domain.RankedFlight, error) {
	limit := q.Limit
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	args := []any{}
	where := "f.collected_at >= $1"
	n := 2
	cutoff := time.Now().UTC().Add(-freshnessWindow(q.Date))
	args = append(args, cutoff)
	if q.Origin != "" {
		where += fmt.Sprintf(" AND r.origin = ANY($%d)", n)
		args = append(args, airportcodes.Codes(q.Origin))
		n++
	}
	if q.Destination != "" {
		where += fmt.Sprintf(" AND r.destination = ANY($%d)", n)
		args = append(args, airportcodes.Codes(q.Destination))
		n++
	}
	if q.Date != "" {
		where += fmt.Sprintf(" AND r.flight_date=$%d::date", n)
		args = append(args, q.Date)
		n++
	}
	if q.MaxStops != "" {
		where += fmt.Sprintf(" AND f.stops <= $%d", n)
		args = append(args, q.MaxStops)
		n++
	}
	weights := score.ForMode(q.Mode)
	if q.CustomWeights {
		weights = score.Weights{Price: q.WeightPrice, Duration: q.WeightDuration, Stops: q.WeightStops, Baggage: q.WeightBaggage, Depart: q.WeightDepart}
	}
	departPref := -1
	if q.PreferDepart && q.DepartMinute >= 0 && q.DepartMinute < 1440 {
		departPref = q.DepartMinute
	} else {
		weights.Depart = 0
	}
	sum := weights.Price + weights.Duration + weights.Stops + weights.Baggage + weights.Depart
	if sum <= 0 {
		weights = score.BestValue()
		sum = 1
	}
	weights.Price, weights.Duration, weights.Stops, weights.Baggage, weights.Depart = weights.Price/sum, weights.Duration/sum, weights.Stops/sum, weights.Baggage/sum, weights.Depart/sum
	args = append(args, weights.Price, weights.Duration, weights.Stops, weights.Baggage, weights.Depart, departPref, limit)
	priceArg, durationArg, stopsArg, baggageArg, departArg, prefArg, limitArg := n, n+1, n+2, n+3, n+4, n+5, n+6
	order := fmt.Sprintf(`(r.price_score*$%d + r.duration_score*$%d + r.stops_score*$%d + r.baggage_score*$%d + (CASE WHEN $%d < 0 THEN 0 ELSE 1 - LEAST(ABS(f.depart_minute-$%d), 1440-ABS(f.depart_minute-$%d))/720.0 END)*$%d) DESC, r.price_score DESC, r.duration_score DESC`, priceArg, durationArg, stopsArg, baggageArg, prefArg, prefArg, prefArg, departArg)
	query := `WITH stats AS (SELECT origin,destination,flight_date, percentile_cont(0.5) WITHIN GROUP (ORDER BY NULLIF(price_toman,0)) AS typical_price FROM flights WHERE collected_at >= $1 GROUP BY origin,destination,flight_date) SELECT f.id,f.origin,f.destination,f.flight_date,f.airline_code,f.airline_name,f.flight_number,f.depart_at,f.arrive_at,f.depart_minute,f.duration_min,f.baggage_label,f.baggage_kg,f.price_rial,f.price_toman,f.is_charter,f.stops,f.source,f.collected_at,f.offer_url,r.price_score,r.duration_score,r.stops_score,r.baggage_score,EXTRACT(EPOCH FROM (now()-f.collected_at))::bigint,COALESCE(ROUND(stats.typical_price),0)::bigint FROM ranked_flights r JOIN flights f ON f.id=r.flight_id LEFT JOIN stats ON stats.origin=f.origin AND stats.destination=f.destination AND stats.flight_date=f.flight_date WHERE ` + where + ` ORDER BY ` + order + ` LIMIT $` + fmt.Sprint(limitArg)
	rows, err := s.pool.Query(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]domain.RankedFlight, 0)
	for rows.Next() {
		var x domain.RankedFlight
		var freshness, typical int64
		if err := rows.Scan(&x.Flight.ID, &x.Flight.Origin, &x.Flight.Destination, &x.Flight.FlightDate, &x.Flight.AirlineCode, &x.Flight.AirlineName, &x.Flight.FlightNumber, &x.Flight.DepartAt, &x.Flight.ArriveAt, &x.Flight.DepartMinute, &x.Flight.DurationMin, &x.Flight.BaggageLabel, &x.Flight.BaggageKG, &x.Flight.PriceRial, &x.Flight.PriceToman, &x.Flight.IsCharter, &x.Flight.Stops, &x.Flight.Source, &x.Flight.CollectedAt, &x.Flight.OfferURL, &x.Scores.Price, &x.Scores.Duration, &x.Scores.Stops, &x.Scores.Baggage, &freshness, &typical); err != nil {
			return nil, err
		}
		x.Rank = len(out) + 1
		depart := 0.0
		if departPref >= 0 {
			depart = score.DepartScore(x.Flight.DepartMinute, departPref)
		}
		x.Score = score.Weights{Price: weights.Price, Duration: weights.Duration, Stops: weights.Stops, Baggage: weights.Baggage, Depart: weights.Depart}.Total(score.Components{Price: x.Scores.Price, Duration: x.Scores.Duration, Stops: x.Scores.Stops, Baggage: x.Scores.Baggage}, depart)
		x.FreshnessSeconds, x.PriceTypicalToman = freshness, typical
		if typical > 0 && x.Flight.PriceToman > 0 {
			x.PriceDeltaPercent = (float64(x.Flight.PriceToman) - float64(typical)) * 100 / float64(typical)
		}
		x.DealLabel, x.VerificationStatus = dealLabel(x.PriceDeltaPercent), verification(freshness, cutoff)
		x.ProviderURL = offerLink(x.Flight.OfferURL)
		x.Recommendation = recommendation(q.Mode, x.Flight)
		out = append(out, x)
	}
	return out, rows.Err()
}

func dealLabel(delta float64) string {
	if delta <= -15 {
		return "great_deal"
	}
	if delta <= -5 {
		return "good_deal"
	}
	if delta >= 20 {
		return "above_typical"
	}
	return "typical_price"
}
func verification(age int64, cutoff time.Time) string {
	if time.Now().UTC().Add(-time.Duration(age) * time.Second).After(cutoff) {
		return "verified"
	}
	return "stale"
}
func recommendation(mode string, f domain.Flight) string {
	switch mode {
	case "cheapest":
		return "lowest_price"
	case "fastest":
		return "shortest_duration"
	case "baggage", "best-baggage":
		return "best_baggage"
	case "best-value", "value":
		return "best_value"
	}
	if f.Stops == 0 {
		return "nonstop_best_value"
	}
	return "best_value"
}
func offerLink(value string) string {
	value = strings.TrimSpace(value)
	if strings.HasPrefix(value, "https://") || strings.HasPrefix(value, "http://") {
		return value
	}
	return ""
}

func freshnessWindow(date string) time.Duration {
	day, err := time.Parse("2006-01-02", date)
	if err != nil {
		// Same catalog window as flight search: an undated query must not
		// require the collector to have written the row in the last 15 minutes.
		return 72 * time.Hour
	}
	today := time.Now().UTC().Truncate(24 * time.Hour)
	days := int(day.Sub(today).Hours() / 24)
	if days <= 1 {
		return 6 * time.Hour
	}
	if days <= 7 {
		return 24 * time.Hour
	}
	return 72 * time.Hour
}
func (s *Store) Ping(ctx context.Context) error { return s.pool.Ping(ctx) }
