package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/joho/godotenv"

	"github.com/airbar/flight-service/shared/domain/flight"
	"github.com/airbar/flight-service/shared/pkg/env"
	"github.com/airbar/flight-service/shared/pkg/postgres"
)

type config struct {
	database, elasticURL, index, worker string
	batch                               int
	poll, retry                         time.Duration
}

func loadConfig() config {
	host, _ := os.Hostname()
	return config{
		database:   env.GetString("DATABASE_URL", "postgres://flight:flight@127.0.0.1:5433/flight_services?sslmode=disable"),
		elasticURL: strings.TrimRight(env.GetString("ELASTICSEARCH_URL", "http://localhost:9200"), "/"),
		index:      env.GetString("ELASTICSEARCH_FLIGHTS_INDEX", "flight_offers"),
		worker:     host + "-" + strconv.FormatInt(time.Now().UnixNano(), 10),
		batch:      env.MaxInt(1, env.GetInt("SEARCH_INDEXER_BATCH", 100)),
		poll:       env.GetDuration("SEARCH_INDEXER_POLL_INTERVAL", 2*time.Second),
		retry:      env.GetDuration("SEARCH_INDEXER_RETRY_DELAY", 30*time.Second),
	}
}

type event struct{ ID, FlightID int64 }

func main() {
	_ = godotenv.Load()
	cfg := loadConfig()
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	p, err := pgxpool.New(ctx, cfg.database)
	if err != nil {
		slog.Error("postgres connect", "err", err)
		os.Exit(1)
	}
	defer p.Close()
	paths := []string{"migrations/001_flights_core.sql"}
	paths = postgres.AppendSiblingMigration(paths, "002_multi_collector.sql")
	paths = postgres.AppendSiblingMigration(paths, "004_maintenance_indexes.sql")
	paths = postgres.AppendSiblingMigration(paths, "005_offer_identity.sql")
	paths = postgres.AppendSiblingMigration(paths, "006_flight_search_outbox.sql")
	paths = postgres.AppendSiblingMigration(paths, "007_ranker_outbox.sql")
	paths = postgres.AppendSiblingMigration(paths, "008_iran_domestic_routes.sql")
	paths = postgres.AppendSiblingMigration(paths, "009_offer_url.sql")
	if err := postgres.RunMigrations(ctx, p, paths); err != nil {
		slog.Error("migrations", "err", err)
		os.Exit(1)
	}
	es := &elastic{base: cfg.elasticURL, index: cfg.index, http: &http.Client{Timeout: 30 * time.Second}}
	if err := es.ensureIndex(ctx); err != nil {
		slog.Warn("elasticsearch index unavailable; events will retry", "err", err)
	}
	slog.Info("flight search indexer started", "index", cfg.index, "worker", cfg.worker)
	for ctx.Err() == nil {
		items, err := claim(ctx, p, cfg.worker, cfg.batch)
		if err != nil {
			slog.Error("claim index events", "err", err)
			sleep(ctx, cfg.retry)
			continue
		}
		if len(items) == 0 {
			sleep(ctx, cfg.poll)
			continue
		}
		for _, item := range items {
			if err := indexOne(ctx, p, es, item); err != nil {
				_ = fail(ctx, p, item.ID, cfg.retry, err.Error())
				slog.Warn("index flight", "event", item.ID, "flight", item.FlightID, "err", err)
				continue
			}
			_, _ = p.Exec(ctx, `DELETE FROM flight_search_outbox WHERE id=$1`, item.ID)
		}
	}
}

func claim(ctx context.Context, p *pgxpool.Pool, worker string, limit int) ([]event, error) {
	tx, err := p.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)
	rows, err := tx.Query(ctx, `
		SELECT id, flight_id FROM flight_search_outbox
		WHERE run_after <= now() AND (locked_at IS NULL OR locked_at < now() - interval '10 minutes')
		ORDER BY id FOR UPDATE SKIP LOCKED LIMIT $1`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []event
	for rows.Next() {
		var e event
		if err := rows.Scan(&e.ID, &e.FlightID); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	for _, e := range out {
		if _, err := tx.Exec(ctx, `UPDATE flight_search_outbox SET locked_by=$2, locked_at=now(), attempts=attempts+1, updated_at=now() WHERE id=$1`, e.ID, worker); err != nil {
			return nil, err
		}
	}
	return out, tx.Commit(ctx)
}

func fail(ctx context.Context, p *pgxpool.Pool, id int64, delay time.Duration, message string) error {
	_, err := p.Exec(ctx, `UPDATE flight_search_outbox SET locked_by=NULL, locked_at=NULL, run_after=now()+$2::interval, last_error=$3, updated_at=now() WHERE id=$1`, id, delay.String(), message)
	return err
}

func indexOne(ctx context.Context, p *pgxpool.Pool, es *elastic, e event) error {
	var f flight.Flight
	err := p.QueryRow(ctx, `SELECT origin,destination,origin_query,destination_query,flight_date,airline_code,airline_name,flight_number,flight_number_norm,depart_at,arrive_at,depart_minute,arrive_minute,baggage_label,baggage_kg,price_rial,price_toman,is_charter,stops,duration_min,proposal_id,source,collected_at FROM flights WHERE id=$1`, e.FlightID).Scan(&f.Origin, &f.Destination, &f.OriginQuery, &f.DestinationQuery, &f.FlightDate, &f.AirlineCode, &f.AirlineName, &f.FlightNumber, &f.FlightNumberNorm, &f.DepartAt, &f.ArriveAt, &f.DepartMinute, &f.ArriveMinute, &f.BaggageLabel, &f.BaggageKG, &f.PriceRial, &f.PriceToman, &f.IsCharter, &f.Stops, &f.DurationMin, &f.ProposalID, &f.Source, &f.CollectedAt)
	if err != nil {
		return err
	}
	body, _ := json.Marshal(f)
	return es.put(ctx, strconv.FormatInt(e.FlightID, 10), body)
}

type elastic struct {
	base, index string
	http        *http.Client
}

func (e *elastic) ensureIndex(ctx context.Context) error {
	mapping := []byte(`{"mappings":{"properties":{"origin":{"type":"keyword"},"destination":{"type":"keyword"},"origin_query":{"type":"keyword"},"destination_query":{"type":"keyword"},"airline_code":{"type":"keyword"},"flight_number_norm":{"type":"keyword"},"flight_date":{"type":"date"},"depart_at":{"type":"date"},"arrive_at":{"type":"date"},"price_toman":{"type":"long"},"stops":{"type":"integer"},"is_charter":{"type":"boolean"},"collected_at":{"type":"date"}}}}`)
	status, body, err := e.request(ctx, http.MethodPut, "/"+e.index, mapping)
	if err != nil {
		return err
	}
	if status >= 400 && status != http.StatusBadRequest {
		return fmt.Errorf("create index: %s", string(body))
	}
	return nil
}
func (e *elastic) put(ctx context.Context, id string, body []byte) error {
	status, data, err := e.request(ctx, http.MethodPut, "/"+e.index+"/_doc/"+id, body)
	if err != nil {
		return err
	}
	if status >= 300 {
		return fmt.Errorf("index document: %s", string(data))
	}
	return nil
}
func (e *elastic) request(ctx context.Context, method, path string, body []byte) (int, []byte, error) {
	req, err := http.NewRequestWithContext(ctx, method, e.base+path, bytes.NewReader(body))
	if err != nil {
		return 0, nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := e.http.Do(req)
	if err != nil {
		return 0, nil, err
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(resp.Body)
	return resp.StatusCode, data, err
}
func sleep(ctx context.Context, d time.Duration) {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
	case <-t.C:
	}
}
