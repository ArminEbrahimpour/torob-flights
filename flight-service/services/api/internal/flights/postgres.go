package flights

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/airbar/flight-service/shared/domain/flight"
	"github.com/airbar/flight-service/shared/pkg/flightnum"
	"github.com/airbar/flight-service/shared/pkg/jalali"
	"github.com/jackc/pgx/v5/pgxpool"
)

type Repository interface {
	Search(ctx context.Context, q flight.SearchQuery) (flight.SearchResult, error)
	Validate(ctx context.Context, req flight.ValidateRequest) (flight.ValidateResponse, error)
	ListUniqueFutureFlights(ctx context.Context, q flight.UniqueFutureFlightsQuery) (flight.UniqueFutureFlightsListResult, error)
	GetFlightByDate(ctx context.Context, q flight.FlightByDateQuery) (flight.FlightByDateResult, error)
	ListSources(ctx context.Context) ([]string, error)
	AddRoute(ctx context.Context, origin, destination string, priority, refreshMinutes, windowDays int) error
	Ping(ctx context.Context) error
}

type PostgresRepository struct {
	pool *pgxpool.Pool
}

func NewPostgresRepository(pool *pgxpool.Pool) *PostgresRepository {
	return &PostgresRepository{pool: pool}
}

func (r *PostgresRepository) Ping(ctx context.Context) error {
	return r.pool.Ping(ctx)
}

func (r *PostgresRepository) ListSources(ctx context.Context) ([]string, error) {
	rows, err := r.pool.Query(ctx, `SELECT DISTINCT source FROM flights ORDER BY source`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var sources []string
	for rows.Next() {
		var s string
		if err := rows.Scan(&s); err != nil {
			return nil, err
		}
		sources = append(sources, s)
	}
	return sources, rows.Err()
}

func (r *PostgresRepository) Search(ctx context.Context, q flight.SearchQuery) (flight.SearchResult, error) {
	if q.Page <= 0 {
		q.Page = 1
	}
	if q.PageSize <= 0 {
		q.PageSize = 50
	}
	if q.PageSize > 200 {
		q.PageSize = 200
	}
	offset := (q.Page - 1) * q.PageSize

	where := []string{"1=1"}
	args := []any{}
	argN := 1
	now := time.Now().UTC()
	maxAge := freshnessWindow(q.Date, now)
	where = append(where, fmt.Sprintf("collected_at >= $%d", argN))
	args = append(args, now.Add(-maxAge))
	argN++

	if q.Origin != "" && q.Destination != "" {
		where = append(where, fmt.Sprintf("(origin_query = $%d OR origin = $%d)", argN, argN))
		args = append(args, strings.ToUpper(q.Origin))
		argN++
		where = append(where, fmt.Sprintf("(destination_query = $%d OR destination = $%d)", argN, argN))
		args = append(args, strings.ToUpper(q.Destination))
		argN++
	}
	if q.Date != "" {
		where = append(where, fmt.Sprintf("flight_date = $%d::date", argN))
		args = append(args, q.Date)
		argN++
	}
	if q.Source != "" && q.Source != "all" {
		where = append(where, fmt.Sprintf("source = $%d", argN))
		args = append(args, q.Source)
		argN++
	}
	if q.Airline != "" {
		where = append(where, fmt.Sprintf("airline_code = $%d", argN))
		args = append(args, strings.ToUpper(q.Airline))
		argN++
	}
	if q.FlightNumber != "" {
		norm := flightnum.Normalize(q.Airline, q.FlightNumber)
		where = append(where, fmt.Sprintf("(flight_number_norm = $%d OR flight_number = $%d)", argN, argN+1))
		args = append(args, norm, strings.TrimSpace(q.FlightNumber))
		argN += 2
	}
	if q.MinPriceToman != nil {
		where = append(where, fmt.Sprintf("price_toman >= $%d", argN))
		args = append(args, *q.MinPriceToman)
		argN++
	}
	if q.MaxPriceToman != nil {
		where = append(where, fmt.Sprintf("price_toman <= $%d", argN))
		args = append(args, *q.MaxPriceToman)
		argN++
	}
	if q.Charter != nil {
		where = append(where, fmt.Sprintf("is_charter = $%d", argN))
		args = append(args, *q.Charter)
		argN++
	}
	if q.MaxStops != nil {
		where = append(where, fmt.Sprintf("stops <= $%d", argN))
		args = append(args, *q.MaxStops)
		argN++
	}

	whereSQL := strings.Join(where, " AND ")
	countQuery := fmt.Sprintf(`SELECT count(*) FROM flights WHERE %s`, whereSQL)
	var total int
	if err := r.pool.QueryRow(ctx, countQuery, args...).Scan(&total); err != nil {
		return flight.SearchResult{}, err
	}

	listQuery := fmt.Sprintf(`
		SELECT origin, destination, origin_query, destination_query, flight_date,
		       airline_code, airline_name, flight_number, flight_number_norm,
		       depart_at, arrive_at, depart_minute, arrive_minute,
		       baggage_label, baggage_kg, price_rial, price_toman,
		       is_charter, stops, duration_min, proposal_id, source, collected_at
		FROM flights WHERE %s
		ORDER BY depart_at ASC
		LIMIT $%d OFFSET $%d`, whereSQL, argN, argN+1)
	args = append(args, q.PageSize, offset)

	rows, err := r.pool.Query(ctx, listQuery, args...)
	if err != nil {
		return flight.SearchResult{}, err
	}
	defer rows.Close()

	result := flight.SearchResult{
		Origin:                 q.Origin,
		Destination:            q.Destination,
		FlightDate:             q.Date,
		Source:                 q.Source,
		Total:                  total,
		Page:                   q.Page,
		PageSize:               q.PageSize,
		FreshnessMaxAgeSeconds: int(maxAge / time.Second),
		FreshnessAsOf:          now,
	}
	for rows.Next() {
		var f flight.Flight
		if err := rows.Scan(
			&f.Origin, &f.Destination, &f.OriginQuery, &f.DestinationQuery, &f.FlightDate,
			&f.AirlineCode, &f.AirlineName, &f.FlightNumber, &f.FlightNumberNorm,
			&f.DepartAt, &f.ArriveAt, &f.DepartMinute, &f.ArriveMinute,
			&f.BaggageLabel, &f.BaggageKG, &f.PriceRial, &f.PriceToman,
			&f.IsCharter, &f.Stops, &f.DurationMin, &f.ProposalID, &f.Source, &f.CollectedAt,
		); err != nil {
			return flight.SearchResult{}, err
		}
		result.Offers = append(result.Offers, f)
	}
	if err := rows.Err(); err != nil {
		return flight.SearchResult{}, err
	}
	if result.Total == 0 && q.Origin != "" && q.Destination != "" && q.Date != "" {
		result.RefreshRequested = r.enqueueRefresh(ctx, q.Origin, q.Destination, q.Date)
	}
	return result, nil
}

// enqueueRefresh requests recollection without calling providers inline. The
// collector-side partial unique index deduplicates concurrent search requests.
func (r *PostgresRepository) enqueueRefresh(ctx context.Context, origin, destination, date string) bool {
	refreshCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	sources := []string{"alibaba", "tahagasht", "snapp", "mrbilit", "safarmarket", "flytoday", "trip"}
	requested := false
	for _, source := range sources {
		var inserted int64
		err := r.pool.QueryRow(refreshCtx, `
			INSERT INTO jobs (source, kind, status, priority, route_id, origin, destination, flight_date, run_after)
			SELECT $3, 'collect_day', 'pending', 1, r.id, r.origin, r.destination, $4::date, now()
			FROM routes r
			WHERE r.enabled AND r.origin = $1 AND r.destination = $2
			  AND NOT EXISTS (
				SELECT 1 FROM jobs j
				WHERE j.source = $3 AND j.kind = 'collect_day'
				  AND j.origin = $1 AND j.destination = $2 AND j.flight_date = $4::date
				  AND j.status IN ('pending', 'running')
			  )
			RETURNING id`, origin, destination, source, date).Scan(&inserted)
		if err == nil && inserted > 0 {
			requested = true
		}
	}
	return requested
}

func (r *PostgresRepository) Validate(ctx context.Context, req flight.ValidateRequest) (flight.ValidateResponse, error) {
	if req.Origin == "" || req.Destination == "" || req.Date == "" || req.FlightNumber == "" {
		return flight.ValidateResponse{Valid: false, Reason: "origin, destination, date, flight_number are required"}, nil
	}
	day, err := jalali.Parse(req.Date)
	if err != nil {
		return flight.ValidateResponse{Valid: false, Reason: "invalid date"}, nil
	}
	origins, err := r.expandAirportCodes(ctx, req.Origin)
	if err != nil {
		return flight.ValidateResponse{}, err
	}
	dests, err := r.expandAirportCodes(ctx, req.Destination)
	if err != nil {
		return flight.ValidateResponse{}, err
	}

	tol := req.TimeTolerance
	if tol <= 0 {
		tol = 30
	}
	var departMin *int
	if req.DepartTime != "" {
		m, err := parseHHMM(req.DepartTime)
		if err != nil {
			return flight.ValidateResponse{Valid: false, Reason: "invalid depart_time, use HH:MM"}, nil
		}
		departMin = &m
	}

	norm := flightnum.Normalize(req.AirlineCode, req.FlightNumber)
	matches, err := r.validateQuery(ctx, origins, dests, day, norm, strings.TrimSpace(req.FlightNumber), req.AirlineCode, departMin, tol)
	if err != nil {
		return flight.ValidateResponse{}, err
	}
	if len(matches) == 0 {
		return flight.ValidateResponse{
			Valid:  false,
			Reason: fmt.Sprintf("no matching flight for %s %s->%s on %s", req.FlightNumber, req.Origin, req.Destination, day.Format("2006-01-02")),
		}, nil
	}
	return flight.ValidateResponse{Valid: true, Matches: matches}, nil
}

func (r *PostgresRepository) validateQuery(ctx context.Context, origins, dests []string, day time.Time, flightNumberNorm, flightNumberRaw, airline string, departMinute *int, tolerance int) ([]flight.ValidateMatch, error) {
	q := `
		SELECT origin, destination, airline_code, airline_name, flight_number,
		       depart_at, arrive_at, baggage_label, baggage_kg, price_toman, is_charter, collected_at
		FROM flights
		WHERE flight_date = $1::date
		  AND origin = ANY($2)
		  AND destination = ANY($3)
		  AND (
		        flight_number_norm = $4
		     OR flight_number = $5
		     OR flight_number_norm = $5
		     OR regexp_replace(flight_number, '\D', '', 'g') = regexp_replace($5, '\D', '', 'g')
		  )`
	args := []any{day.Format("2006-01-02"), origins, dests, flightNumberNorm, flightNumberRaw}
	argN := 6
	if airline != "" {
		q += fmt.Sprintf(" AND airline_code = $%d", argN)
		args = append(args, strings.ToUpper(airline))
		argN++
	}
	if departMinute != nil {
		q += fmt.Sprintf(" AND ABS(depart_minute - $%d) <= $%d", argN, argN+1)
		args = append(args, *departMinute, tolerance)
	}
	q += " ORDER BY collected_at DESC LIMIT 50"

	rows, err := r.pool.Query(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []flight.ValidateMatch
	for rows.Next() {
		var m flight.ValidateMatch
		if err := rows.Scan(&m.Origin, &m.Destination, &m.AirlineCode, &m.AirlineName, &m.FlightNumber,
			&m.DepartAt, &m.ArriveAt, &m.Baggage, &m.BaggageKG, &m.PriceToman, &m.IsCharter, &m.CollectedAt); err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

func (r *PostgresRepository) expandAirportCodes(ctx context.Context, code string) ([]string, error) {
	code = strings.ToUpper(strings.TrimSpace(code))
	rows, err := r.pool.Query(ctx, `
		SELECT airport_code FROM airport_aliases WHERE alias_code = $1
		UNION
		SELECT $1 WHERE EXISTS (SELECT 1 FROM airports WHERE code = $1)`, code)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var codes []string
	for rows.Next() {
		var c string
		if err := rows.Scan(&c); err != nil {
			return nil, err
		}
		codes = append(codes, c)
	}
	if len(codes) == 0 {
		codes = []string{code}
	}
	return codes, rows.Err()
}

func (r *PostgresRepository) AddRoute(ctx context.Context, origin, destination string, priority, refreshMinutes, windowDays int) error {
	origin = strings.ToUpper(strings.TrimSpace(origin))
	destination = strings.ToUpper(strings.TrimSpace(destination))
	_, err := r.pool.Exec(ctx, `
		INSERT INTO airports (code) VALUES ($1), ($2)
		ON CONFLICT DO NOTHING`, origin, destination)
	if err != nil {
		return err
	}
	_, err = r.pool.Exec(ctx, `
		INSERT INTO routes (origin, destination, priority, refresh_minutes, window_days)
		VALUES ($1,$2,$3,$4,$5)
		ON CONFLICT (origin, destination) DO UPDATE SET
			priority=EXCLUDED.priority,
			refresh_minutes=EXCLUDED.refresh_minutes,
			window_days=EXCLUDED.window_days,
			enabled=TRUE`,
		origin, destination, priority, refreshMinutes, windowDays)
	return err
}

func (r *PostgresRepository) ListUniqueFutureFlights(ctx context.Context, q flight.UniqueFutureFlightsQuery) (flight.UniqueFutureFlightsListResult, error) {
	if q.Page <= 0 {
		q.Page = 1
	}
	if q.PageSize <= 0 {
		q.PageSize = 50
	}
	if q.PageSize > 200 {
		q.PageSize = 200
	}
	offset := (q.Page - 1) * q.PageSize

	where := []string{"depart_at > NOW()"}
	args := []any{}
	argN := 1

	flightNumber := strings.TrimSpace(q.FlightNumber)
	if flightNumber != "" {
		norm := flightnum.Normalize("", flightNumber)
		where = append(where, fmt.Sprintf("flight_number_norm = $%d", argN))
		args = append(args, norm)
		argN++
	}

	whereSQL := strings.Join(where, " AND ")

	countQuery := fmt.Sprintf(`
		SELECT count(*) FROM (
			SELECT 1
			FROM flights
			WHERE %s
			GROUP BY flight_date
		) scheduled`, whereSQL)
	var total int
	if err := r.pool.QueryRow(ctx, countQuery, args...).Scan(&total); err != nil {
		return flight.UniqueFutureFlightsListResult{}, err
	}

	listQuery := fmt.Sprintf(`
		SELECT
			origin, destination, flight_date,
			airline_code, airline_name, flight_number, flight_number_norm,
			depart_at, arrive_at, baggage_kg, stops, is_charter, price_toman
		FROM (
			SELECT DISTINCT ON (flight_date)
				origin, destination, flight_date,
				airline_code, airline_name, flight_number, flight_number_norm,
				depart_at, arrive_at, baggage_kg, stops, is_charter, price_toman
			FROM flights
			WHERE %s
			ORDER BY
				flight_date,
				depart_at ASC,
				CASE WHEN origin LIKE '%%ALL' THEN 1 ELSE 0 END,
				NULLIF(price_toman, 0) ASC NULLS LAST,
				collected_at DESC
		) scheduled
		ORDER BY depart_at ASC
		LIMIT $%d OFFSET $%d`, whereSQL, argN, argN+1)
	listArgs := append(args, q.PageSize, offset)

	rows, err := r.pool.Query(ctx, listQuery, listArgs...)
	if err != nil {
		return flight.UniqueFutureFlightsListResult{}, err
	}
	defer rows.Close()

	result := flight.UniqueFutureFlightsListResult{
		FlightNumber: flightNumber,
		Total:        total,
		Page:         q.Page,
		PageSize:     q.PageSize,
	}
	for rows.Next() {
		var item flight.UniqueFutureFlightsResult
		var flightDate time.Time
		if err := rows.Scan(
			&item.Origin, &item.Destination, &flightDate,
			&item.AirlineCode, &item.AirlineName, &item.FlightNumber, &item.FlightNumberNorm,
			&item.DepartAt, &item.ArriveAt, &item.BaggageKG, &item.Stops, &item.IsCharter, &item.PriceToman,
		); err != nil {
			return flight.UniqueFutureFlightsListResult{}, err
		}
		item.FlightDate = flightDate.Format("2006-01-02")
		result.Flights = append(result.Flights, item)
	}
	return result, rows.Err()
}

func (r *PostgresRepository) GetFlightByDate(ctx context.Context, q flight.FlightByDateQuery) (flight.FlightByDateResult, error) {
	number := strings.TrimSpace(q.FlightNumber)
	if number == "" || strings.TrimSpace(q.Date) == "" {
		return flight.FlightByDateResult{}, fmt.Errorf("flight_number and date are required")
	}
	day, err := jalali.Parse(q.Date)
	if err != nil {
		return flight.FlightByDateResult{}, fmt.Errorf("invalid date")
	}
	norm := flightnum.Normalize("", number)

	const query = `
		SELECT origin, destination, origin_query, destination_query, flight_date,
		       airline_code, airline_name, flight_number, flight_number_norm,
		       depart_at, arrive_at, depart_minute, arrive_minute,
		       baggage_label, baggage_kg, price_rial, price_toman,
		       is_charter, stops, duration_min, proposal_id, source, collected_at
		FROM flights
		WHERE flight_date = $1::date
		  AND (flight_number_norm = $2 OR flight_number = $3)
		ORDER BY collected_at DESC
		LIMIT 1`

	var f flight.Flight
	err = r.pool.QueryRow(ctx, query, day.Format("2006-01-02"), norm, number).Scan(
		&f.Origin, &f.Destination, &f.OriginQuery, &f.DestinationQuery, &f.FlightDate,
		&f.AirlineCode, &f.AirlineName, &f.FlightNumber, &f.FlightNumberNorm,
		&f.DepartAt, &f.ArriveAt, &f.DepartMinute, &f.ArriveMinute,
		&f.BaggageLabel, &f.BaggageKG, &f.PriceRial, &f.PriceToman,
		&f.IsCharter, &f.Stops, &f.DurationMin, &f.ProposalID, &f.Source, &f.CollectedAt,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return flight.FlightByDateResult{Found: false}, nil
		}
		return flight.FlightByDateResult{}, err
	}
	return flight.FlightByDateResult{Found: true, Flight: &f}, nil
}

func parseHHMM(v string) (int, error) {
	parts := strings.Split(strings.TrimSpace(v), ":")
	if len(parts) != 2 {
		return 0, fmt.Errorf("bad time")
	}
	var h, m int
	if _, err := fmt.Sscanf(parts[0], "%d", &h); err != nil {
		return 0, err
	}
	if _, err := fmt.Sscanf(parts[1], "%d", &m); err != nil {
		return 0, err
	}
	if h < 0 || h > 23 || m < 0 || m > 59 {
		return 0, fmt.Errorf("bad time")
	}
	return h*60 + m, nil
}
