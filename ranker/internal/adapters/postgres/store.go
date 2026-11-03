package postgres

import (
	"context"
	"fmt"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/torob/ranker/internal/domain"
	"net/url"
	"sort"
	"strings"
	"time"
)

type Store struct{ pool *pgxpool.Pool }

func New(pool *pgxpool.Pool) *Store { return &Store{pool: pool} }
func (s *Store) Upsert(ctx context.Context, f domain.Flight) error {
	_, err := s.pool.Exec(ctx, `INSERT INTO flights (id,origin,destination,flight_date,airline_code,airline_name,flight_number,depart_at,arrive_at,depart_minute,duration_min,baggage_label,baggage_kg,price_rial,price_toman,is_charter,stops,source,collected_at,updated_at) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18,$19,now()) ON CONFLICT (id) DO UPDATE SET origin=EXCLUDED.origin,destination=EXCLUDED.destination,flight_date=EXCLUDED.flight_date,airline_code=EXCLUDED.airline_code,airline_name=EXCLUDED.airline_name,flight_number=EXCLUDED.flight_number,depart_at=EXCLUDED.depart_at,arrive_at=EXCLUDED.arrive_at,depart_minute=EXCLUDED.depart_minute,duration_min=EXCLUDED.duration_min,baggage_label=EXCLUDED.baggage_label,baggage_kg=EXCLUDED.baggage_kg,price_rial=EXCLUDED.price_rial,price_toman=EXCLUDED.price_toman,is_charter=EXCLUDED.is_charter,stops=EXCLUDED.stops,source=EXCLUDED.source,collected_at=EXCLUDED.collected_at,updated_at=now()`, f.ID, f.Origin, f.Destination, f.FlightDate, f.AirlineCode, f.AirlineName, f.FlightNumber, f.DepartAt, f.ArriveAt, f.DepartMinute, f.DurationMin, f.BaggageLabel, f.BaggageKG, f.PriceRial, f.PriceToman, f.IsCharter, f.Stops, f.Source, f.CollectedAt)
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
		sort.SliceStable(flights, func(i, j int) bool { return less(flights[i], flights[j]) })
		tx, err := s.pool.Begin(ctx)
		if err != nil {
			return err
		}
		for rank, f := range flights {
			score := scoreOf(f, rank)
			if _, err = tx.Exec(ctx, `INSERT INTO ranked_flights (flight_id,origin,destination,flight_date,rank,score,ranked_at) VALUES ($1,$2,$3,$4,$5,$6,now()) ON CONFLICT (flight_id) DO UPDATE SET origin=EXCLUDED.origin,destination=EXCLUDED.destination,flight_date=EXCLUDED.flight_date,rank=EXCLUDED.rank,score=EXCLUDED.score,ranked_at=now()`, f.ID, f.Origin, f.Destination, f.FlightDate, rank+1, score); err != nil {
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
func scoreOf(f domain.Flight, rank int) float64 {
	b := 0.
	if f.BaggageKG != nil {
		b = float64(*f.BaggageKG)
	}
	stops := float64(f.Stops)
	duration := 0.0
	if f.DurationMin != nil && *f.DurationMin > 0 {
		duration = float64(*f.DurationMin)
	}
	charter := 0.0
	if f.IsCharter {
		charter = 1
	}
	// The persisted rank is authoritative; this score is an explainability hint.
	return 1000000 - float64(f.PriceToman) - stops*5000 - duration*10 + b*100 - charter*250 - float64(f.DepartMinute)/10 - float64(rank)
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
		where += fmt.Sprintf(" AND r.origin=$%d", n)
		args = append(args, q.Origin)
		n++
	}
	if q.Destination != "" {
		where += fmt.Sprintf(" AND r.destination=$%d", n)
		args = append(args, q.Destination)
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
	args = append(args, limit)
	order := "r.rank"
	switch q.Mode {
	case "cheapest":
		order = "NULLIF(f.price_toman,0) ASC NULLS LAST, f.stops ASC, f.depart_at ASC"
	case "fastest":
		order = "f.duration_min ASC NULLS LAST, f.stops ASC, NULLIF(f.price_toman,0) ASC NULLS LAST"
	case "baggage", "best-baggage":
		order = "COALESCE(f.baggage_kg,0) DESC, NULLIF(f.price_toman,0) ASC NULLS LAST, f.stops ASC"
	case "best-value", "value":
		order = "r.rank"
	}
	query := `WITH stats AS (SELECT origin,destination,flight_date, percentile_cont(0.5) WITHIN GROUP (ORDER BY NULLIF(price_toman,0)) AS typical_price FROM flights WHERE collected_at >= $1 GROUP BY origin,destination,flight_date) SELECT f.id,f.origin,f.destination,f.flight_date,f.airline_code,f.airline_name,f.flight_number,f.depart_at,f.arrive_at,f.depart_minute,f.duration_min,f.baggage_label,f.baggage_kg,f.price_rial,f.price_toman,f.is_charter,f.stops,f.source,f.collected_at,r.rank,r.score,EXTRACT(EPOCH FROM (now()-f.collected_at))::bigint,COALESCE(ROUND(stats.typical_price),0)::bigint FROM ranked_flights r JOIN flights f ON f.id=r.flight_id LEFT JOIN stats ON stats.origin=f.origin AND stats.destination=f.destination AND stats.flight_date=f.flight_date WHERE ` + where + ` ORDER BY ` + order + ` LIMIT $` + fmt.Sprint(n)
	rows, err := s.pool.Query(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]domain.RankedFlight, 0)
	for rows.Next() {
		var x domain.RankedFlight
		var freshness, typical int64
		if err := rows.Scan(&x.Flight.ID, &x.Flight.Origin, &x.Flight.Destination, &x.Flight.FlightDate, &x.Flight.AirlineCode, &x.Flight.AirlineName, &x.Flight.FlightNumber, &x.Flight.DepartAt, &x.Flight.ArriveAt, &x.Flight.DepartMinute, &x.Flight.DurationMin, &x.Flight.BaggageLabel, &x.Flight.BaggageKG, &x.Flight.PriceRial, &x.Flight.PriceToman, &x.Flight.IsCharter, &x.Flight.Stops, &x.Flight.Source, &x.Flight.CollectedAt, &x.Rank, &x.Score, &freshness, &typical); err != nil {
			return nil, err
		}
		x.FreshnessSeconds, x.PriceTypicalToman = freshness, typical
		if typical > 0 && x.Flight.PriceToman > 0 {
			x.PriceDeltaPercent = (float64(x.Flight.PriceToman) - float64(typical)) * 100 / float64(typical)
		}
		x.DealLabel, x.VerificationStatus = dealLabel(x.PriceDeltaPercent), verification(freshness, cutoff)
		x.ProviderURL = providerURL(x.Flight.Source, x.Flight.Origin, x.Flight.Destination, x.Flight.FlightDate.Format("2006-01-02"))
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
func providerURL(source, origin, destination, date string) string {
	s := strings.ToLower(source)
	q := url.Values{"origin": {origin}, "destination": {destination}, "date": {date}}
	switch s {
	case "alibaba":
		return "https://www.alibaba.ir/flights?" + q.Encode()
	case "flytoday":
		return "https://www.flytoday.ir/flights?" + q.Encode()
	case "mrbilit":
		return "https://mrbilit.com/flight?" + q.Encode()
	case "snapp":
		return "https://flights.snapp.ir/?" + q.Encode()
	case "safarmarket":
		return "https://safarmarket.com/flights?" + q.Encode()
	case "trip":
		return "https://www.trip.ir/flights?" + q.Encode()
	default:
		return ""
	}
}

func freshnessWindow(date string) time.Duration {
	day, err := time.Parse("2006-01-02", date)
	if err != nil {
		return 15 * time.Minute
	}
	today := time.Now().UTC().Truncate(24 * time.Hour)
	days := int(day.Sub(today).Hours() / 24)
	if days <= 1 {
		return 5 * time.Minute
	}
	if days <= 7 {
		return 15 * time.Minute
	}
	return 60 * time.Minute
}
func (s *Store) Ping(ctx context.Context) error { return s.pool.Ping(ctx) }
