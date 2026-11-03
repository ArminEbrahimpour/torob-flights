-- Durable events for the ranker read model. The payload contains the complete
-- flight row so the ranker can maintain its own database without querying the
-- collectors' database.
CREATE TABLE IF NOT EXISTS flight_ranker_outbox (
    id BIGSERIAL PRIMARY KEY,
    flight_id BIGINT NOT NULL REFERENCES flights(id) ON DELETE CASCADE,
    operation TEXT NOT NULL DEFAULT 'upsert',
    payload JSONB NOT NULL,
    attempts INT NOT NULL DEFAULT 0,
    run_after TIMESTAMPTZ NOT NULL DEFAULT now(),
    locked_by TEXT,
    locked_at TIMESTAMPTZ,
    last_error TEXT,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (flight_id)
);

CREATE INDEX IF NOT EXISTS idx_flight_ranker_outbox_claim
    ON flight_ranker_outbox (run_after, id) WHERE locked_at IS NULL;

CREATE OR REPLACE FUNCTION enqueue_flight_ranker_event()
RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    INSERT INTO flight_ranker_outbox (flight_id, operation, payload, run_after, updated_at)
    VALUES (NEW.id, 'upsert', to_jsonb(NEW), now(), now())
    ON CONFLICT (flight_id) DO UPDATE SET
        operation = 'upsert', payload = EXCLUDED.payload, run_after = now(),
        locked_by = NULL, locked_at = NULL, last_error = NULL, updated_at = now();
    RETURN NEW;
END;
$$;

DROP TRIGGER IF EXISTS flights_ranker_outbox_trigger ON flights;
CREATE TRIGGER flights_ranker_outbox_trigger
AFTER INSERT OR UPDATE ON flights FOR EACH ROW
EXECUTE FUNCTION enqueue_flight_ranker_event();

INSERT INTO flight_ranker_outbox (flight_id, payload)
SELECT id, to_jsonb(f) FROM flights f
ON CONFLICT (flight_id) DO NOTHING;
