package jobs

import (
	"context"
	"encoding/json"
	"time"

	"github.com/airbar/flight-service/shared/domain/flight"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

const SourceAlibaba = "alibaba"

type Store struct {
	pool *pgxpool.Pool
}

func NewStore(pool *pgxpool.Pool) *Store {
	return &Store{pool: pool}
}

func (s *Store) ListEnabledRoutes(ctx context.Context) ([]flight.Route, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT r.id, r.origin, r.destination, r.enabled, r.priority, r.refresh_minutes, r.window_days,
		       crs.last_enqueued_at
		FROM routes r
		LEFT JOIN collector_route_state crs ON crs.route_id = r.id AND crs.source = 'alibaba'
		WHERE r.enabled = TRUE ORDER BY r.priority, r.id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []flight.Route
	for rows.Next() {
		var r flight.Route
		if err := rows.Scan(&r.ID, &r.Origin, &r.Destination, &r.Enabled, &r.Priority, &r.RefreshMinutes, &r.WindowDays, &r.LastEnqueuedAt); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

func (s *Store) MarkRouteEnqueued(ctx context.Context, routeID int64) error {
	_, err := s.pool.Exec(ctx, `
		WITH state AS (
			INSERT INTO collector_route_state (source, route_id, last_enqueued_at)
			VALUES ('alibaba', $1, now())
			ON CONFLICT (source, route_id)
			DO UPDATE SET last_enqueued_at = EXCLUDED.last_enqueued_at
			RETURNING route_id, last_enqueued_at
		)
		UPDATE routes r
		SET last_enqueued_at = state.last_enqueued_at
		FROM state
		WHERE r.id = state.route_id`, routeID)
	return err
}

func (s *Store) EnqueueSeedJobs(ctx context.Context, routes []flight.Route, collectSpacing time.Duration) (int64, error) {
	var n int64
	if collectSpacing <= 0 {
		collectSpacing = 5 * time.Second
	}
	var cumulativeDays int
	for _, r := range routes {
		runAfter := time.Now().UTC().Add(time.Duration(cumulativeDays) * collectSpacing)
		cumulativeDays += maxInt(1, r.WindowDays)
		tag, err := s.pool.Exec(ctx, `
			INSERT INTO jobs (source, kind, status, priority, route_id, origin, destination, run_after)
			SELECT 'alibaba', 'seed_route', 'pending', $1, $2, $3, $4, $5
			WHERE NOT EXISTS (
				SELECT 1 FROM jobs
				WHERE source = 'alibaba' AND kind = 'seed_route' AND route_id = $2 AND status IN ('pending','running')
			)`, r.Priority, r.ID, r.Origin, r.Destination, runAfter)
		if err != nil {
			return n, err
		}
		n += tag.RowsAffected()
	}
	return n, nil
}

func (s *Store) EnqueueCollectDays(ctx context.Context, route flight.Route, dates []time.Time, spacing time.Duration) (int64, error) {
	var n int64
	if spacing <= 0 {
		spacing = 5 * time.Second
	}
	base := time.Now().UTC()
	for i, d := range dates {
		runAfter := base.Add(time.Duration(i) * spacing)
		tag, err := s.pool.Exec(ctx, `
			INSERT INTO jobs (source, kind, status, priority, route_id, origin, destination, flight_date, run_after)
			SELECT 'alibaba', 'collect_day', 'pending', $1, $2, $3, $4, $5::date, $6
			WHERE NOT EXISTS (
				SELECT 1 FROM jobs
				WHERE source = 'alibaba' AND kind = 'collect_day'
				  AND origin = $3 AND destination = $4 AND flight_date = $5::date
				  AND status IN ('pending', 'running')
			)`,
			route.Priority, route.ID, route.Origin, route.Destination, d.Format("2006-01-02"), runAfter)
		if err != nil {
			return n, err
		}
		n += tag.RowsAffected()
	}
	return n, nil
}

func (s *Store) ClaimJobs(ctx context.Context, workerID string, limit int) ([]flight.Job, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)

	rows, err := tx.Query(ctx, `
		SELECT id, kind, status, priority, route_id, origin, destination, flight_date, attempts, max_attempts, run_after, payload
		FROM jobs
		WHERE source = 'alibaba' AND status = 'pending' AND run_after <= now() AND attempts < max_attempts
		  AND kind IN ('seed_route', 'collect_day')
		ORDER BY priority ASC, id ASC
		FOR UPDATE SKIP LOCKED
		LIMIT $1`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var jobs []flight.Job
	var ids []int64
	for rows.Next() {
		var j flight.Job
		var kind, status string
		var fd *time.Time
		var payload []byte
		if err := rows.Scan(&j.ID, &kind, &status, &j.Priority, &j.RouteID, &j.Origin, &j.Destination, &fd, &j.Attempts, &j.MaxAttempts, &j.RunAfter, &payload); err != nil {
			return nil, err
		}
		j.Kind = flight.JobKind(kind)
		j.Status = flight.JobStatus(status)
		j.FlightDate = fd
		j.Payload = json.RawMessage(payload)
		jobs = append(jobs, j)
		ids = append(ids, j.ID)
	}
	rows.Close()
	if len(ids) == 0 {
		return nil, tx.Commit(ctx)
	}
	_, err = tx.Exec(ctx, `
		UPDATE jobs
		SET status = 'running', locked_by = $2, locked_at = now(), attempts = attempts + 1, updated_at = now()
		WHERE source = 'alibaba' AND id = ANY($1)`, ids, workerID)
	if err != nil {
		return nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	for i := range jobs {
		jobs[i].Attempts++
		jobs[i].Status = flight.JobRunning
	}
	return jobs, nil
}

func (s *Store) CompleteJob(ctx context.Context, jobID int64) error {
	_, err := s.pool.Exec(ctx, `
		UPDATE jobs SET status='done', finished_at=now(), updated_at=now(), locked_by=NULL, locked_at=NULL, last_error=NULL
		WHERE id=$1 AND source='alibaba'`, jobID)
	return err
}

func (s *Store) FailJob(ctx context.Context, jobID int64, attempts, maxAttempts int, errMsg string, retryDelay time.Duration) error {
	if attempts >= maxAttempts {
		_, err := s.pool.Exec(ctx, `
			UPDATE jobs
			SET status='dead', last_error=$2, finished_at=now(), updated_at=now(),
			    locked_by=NULL, locked_at=NULL
			WHERE id=$1 AND source='alibaba'`, jobID, truncate(errMsg, 1000))
		return err
	}
	if retryDelay <= 0 {
		retryDelay = 15 * time.Second
	}
	_, err := s.pool.Exec(ctx, `
		UPDATE jobs
		SET status='pending', last_error=$2, run_after=$3, updated_at=now(),
		    locked_by=NULL, locked_at=NULL
		WHERE id=$1 AND source='alibaba'`, jobID, truncate(errMsg, 1000), time.Now().UTC().Add(retryDelay))
	return err
}

func (s *Store) DeferJob(ctx context.Context, jobID int64, retryDelay time.Duration, reason string) error {
	if retryDelay <= 0 {
		retryDelay = time.Minute
	}
	_, err := s.pool.Exec(ctx, `
		UPDATE jobs
		SET status='pending',
		    attempts=GREATEST(attempts-1, 0),
		    last_error=$2,
		    run_after=$3,
		    updated_at=now(),
		    locked_by=NULL,
		    locked_at=NULL
		WHERE id=$1 AND source='alibaba'`, jobID, truncate(reason, 1000), time.Now().UTC().Add(retryDelay))
	return err
}

func (s *Store) MarkExhaustedDead(ctx context.Context) (int64, error) {
	tag, err := s.pool.Exec(ctx, `
		UPDATE jobs
		SET status='dead', finished_at=now(), updated_at=now(),
		    locked_by=NULL, locked_at=NULL,
		    last_error=COALESCE(last_error, 'maximum attempts exhausted')
		WHERE source='alibaba' AND status='pending' AND attempts >= max_attempts`)
	if err != nil {
		return 0, err
	}
	return tag.RowsAffected(), nil
}

func (s *Store) RequeueFailed(ctx context.Context) (int64, error) {
	tag, err := s.pool.Exec(ctx, `
		UPDATE jobs SET status='pending', updated_at=now()
		WHERE source='alibaba' AND status='failed' AND run_after <= now() AND attempts < max_attempts`)
	if err != nil {
		return 0, err
	}
	return tag.RowsAffected(), nil
}

func (s *Store) ReclaimStale(ctx context.Context, after time.Duration) (int64, error) {
	tag, err := s.pool.Exec(ctx, `
		UPDATE jobs
		SET status='pending', locked_by=NULL, locked_at=NULL, updated_at=now(),
		    last_error = COALESCE(last_error,'') || ' | reclaimed stale lock'
		WHERE source='alibaba' AND status='running' AND locked_at < now() - $1::interval`, after.String())
	if err != nil {
		return 0, err
	}
	return tag.RowsAffected(), nil
}

func (s *Store) UpsertFlights(ctx context.Context, flights []flight.Flight) (int64, error) {
	if len(flights) == 0 {
		return 0, nil
	}
	batch := &pgx.Batch{}
	const q = `
		INSERT INTO flights (
			origin, destination, origin_query, destination_query, flight_date,
			airline_code, airline_name, flight_number, flight_number_norm,
			depart_at, arrive_at, depart_minute, arrive_minute,
			baggage_label, baggage_kg, price_rial, price_toman,
			is_charter, stops, duration_min, proposal_id, offer_url, source, collected_at, updated_at
		) VALUES (
			$1,$2,$3,$4,$5::date,
			$6,$7,$8,$9,
			$10,$11,$12,$13,
			$14,$15,$16,$17,
			$18,$19,$20,$21,$22,$23, now(), now()
		)
		ON CONFLICT (
			source, origin, destination, flight_date, airline_code, flight_number,
			depart_at, arrive_at, price_rial, is_charter, proposal_id
		) DO UPDATE SET
			airline_name = EXCLUDED.airline_name,
			flight_number_norm = EXCLUDED.flight_number_norm,
			baggage_label = EXCLUDED.baggage_label,
			baggage_kg = EXCLUDED.baggage_kg,
			duration_min = EXCLUDED.duration_min,
			proposal_id = EXCLUDED.proposal_id,
			offer_url = EXCLUDED.offer_url,
			origin_query = EXCLUDED.origin_query,
			destination_query = EXCLUDED.destination_query,
			collected_at = now(),
			updated_at = now()`
	for _, f := range flights {
		source := f.Source
		if source == "" {
			source = SourceAlibaba
		}
		batch.Queue(q,
			f.Origin, f.Destination, f.OriginQuery, f.DestinationQuery, f.FlightDate.Format("2006-01-02"),
			f.AirlineCode, f.AirlineName, f.FlightNumber, f.FlightNumberNorm,
			f.DepartAt, f.ArriveAt, f.DepartMinute, f.ArriveMinute,
			f.BaggageLabel, f.BaggageKG, f.PriceRial, f.PriceToman,
			f.IsCharter, f.Stops, f.DurationMin, f.ProposalID, f.OfferURL, source,
		)
	}
	br := s.pool.SendBatch(ctx, batch)
	defer br.Close()
	var n int64
	for range flights {
		tag, err := br.Exec()
		if err != nil {
			return n, err
		}
		n += tag.RowsAffected()
	}
	return n, nil
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}
