package main

import (
	"context"
	"log/slog"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	amqp "github.com/rabbitmq/amqp091-go"

	"github.com/airbar/flight-service/shared/pkg/env"
	"github.com/airbar/flight-service/shared/pkg/postgres"
)

type config struct {
	db, rabbit, exchange, routing, worker string
	batch                                 int
	poll, retry                           time.Duration
}

func loadConfig() config {
	host, _ := os.Hostname()
	return config{db: env.GetString("DATABASE_URL", "postgres://flight:flight@localhost:5432/flight_services?sslmode=disable"), rabbit: env.GetString("RABBITMQ_URL", "amqp://guest:guest@localhost:5672/"), exchange: env.GetString("RANKER_RABBITMQ_EXCHANGE", "torob.flights"), routing: env.GetString("RANKER_RABBITMQ_ROUTING_KEY", "flight.upsert"), worker: host + "-" + strconv.FormatInt(time.Now().UnixNano(), 10), batch: env.MaxInt(1, env.GetInt("RANKER_PUBLISHER_BATCH", 100)), poll: env.GetDuration("RANKER_PUBLISHER_POLL_INTERVAL", 2*time.Second), retry: env.GetDuration("RANKER_PUBLISHER_RETRY_DELAY", 30*time.Second)}
}

type event struct {
	ID, FlightID int64
	Payload      []byte
}

func main() {
	cfg := loadConfig()
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	p, err := pgxpool.New(ctx, cfg.db)
	if err != nil {
		slog.Error("postgres connect", "err", err)
		os.Exit(1)
	}
	defer p.Close()
	paths := []string{"migrations/001_flights_core.sql"}
	for _, name := range []string{"002_multi_collector.sql", "004_maintenance_indexes.sql", "005_offer_identity.sql", "006_flight_search_outbox.sql", "007_ranker_outbox.sql", "008_iran_domestic_routes.sql"} {
		paths = postgres.AppendSiblingMigration(paths, name)
	}
	if err := postgres.RunMigrations(ctx, p, paths); err != nil {
		slog.Error("migrations", "err", err)
		os.Exit(1)
	}
	for ctx.Err() == nil {
		conn, channel, err := connect(ctx, cfg)
		if err != nil {
			slog.Warn("rabbitmq connect", "err", err)
			sleep(ctx, cfg.retry)
			continue
		}
		err = publishLoop(ctx, p, channel, cfg)
		_ = channel.Close()
		_ = conn.Close()
		if err != nil && ctx.Err() == nil {
			slog.Warn("publisher stopped", "err", err)
			sleep(ctx, cfg.retry)
		}
	}
}

func connect(ctx context.Context, cfg config) (*amqp.Connection, *amqp.Channel, error) {
	conn, err := amqp.DialConfig(cfg.rabbit, amqp.Config{Properties: amqp.NewConnectionProperties()})
	if err != nil {
		return nil, nil, err
	}
	ch, err := conn.Channel()
	if err != nil {
		_ = conn.Close()
		return nil, nil, err
	}
	if err = ch.ExchangeDeclare(cfg.exchange, amqp.ExchangeTopic, true, false, false, false, nil); err != nil {
		_ = ch.Close()
		_ = conn.Close()
		return nil, nil, err
	}
	return conn, ch, nil
}

func publishLoop(ctx context.Context, p *pgxpool.Pool, ch *amqp.Channel, cfg config) error {
	for ctx.Err() == nil {
		items, err := claim(ctx, p, cfg.worker, cfg.batch)
		if err != nil {
			return err
		}
		if len(items) == 0 {
			sleep(ctx, cfg.poll)
			continue
		}
		for _, item := range items {
			if err := ch.PublishWithContext(ctx, cfg.exchange, cfg.routing, false, false, amqp.Publishing{ContentType: "application/json", DeliveryMode: amqp.Persistent, Body: item.Payload, Timestamp: time.Now().UTC()}); err != nil {
				_ = fail(ctx, p, item.ID, cfg.retry, err.Error())
				return err
			}
			if _, err := p.Exec(ctx, `DELETE FROM flight_ranker_outbox WHERE id=$1`, item.ID); err != nil {
				return err
			}
		}
	}
	return ctx.Err()
}
func claim(ctx context.Context, p *pgxpool.Pool, worker string, limit int) ([]event, error) {
	tx, err := p.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)
	rows, err := tx.Query(ctx, `SELECT id, flight_id, payload FROM flight_ranker_outbox WHERE run_after <= now() AND (locked_at IS NULL OR locked_at < now()-interval '10 minutes') ORDER BY id FOR UPDATE SKIP LOCKED LIMIT $1`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []event
	for rows.Next() {
		var e event
		if err := rows.Scan(&e.ID, &e.FlightID, &e.Payload); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	for _, e := range out {
		if _, err := tx.Exec(ctx, `UPDATE flight_ranker_outbox SET locked_by=$2, locked_at=now(), attempts=attempts+1, updated_at=now() WHERE id=$1`, e.ID, worker); err != nil {
			return nil, err
		}
	}
	return out, tx.Commit(ctx)
}
func fail(ctx context.Context, p *pgxpool.Pool, id int64, delay time.Duration, msg string) error {
	_, err := p.Exec(ctx, `UPDATE flight_ranker_outbox SET locked_by=NULL, locked_at=NULL, run_after=now()+$2::interval, last_error=$3, updated_at=now() WHERE id=$1`, id, delay.String(), msg)
	return err
}
func sleep(ctx context.Context, d time.Duration) {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
	case <-t.C:
	}
}
