-- Transactional bridge from PostgreSQL to the Elasticsearch search read model.
-- The trigger runs in the same transaction as the flight write, so a committed
-- flight always has an indexing event and a failed write never leaks an event.
CREATE TABLE IF NOT EXISTS flight_search_outbox (
    id          BIGSERIAL PRIMARY KEY,
    flight_id   BIGINT NOT NULL REFERENCES flights(id) ON DELETE CASCADE,
    operation   TEXT NOT NULL DEFAULT 'upsert',
    attempts    INT NOT NULL DEFAULT 0,
    run_after   TIMESTAMPTZ NOT NULL DEFAULT now(),
    locked_by   TEXT,
    locked_at   TIMESTAMPTZ,
    last_error  TEXT,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (flight_id)
);

CREATE INDEX IF NOT EXISTS idx_flight_search_outbox_claim
    ON flight_search_outbox (run_after, id)
    WHERE locked_at IS NULL;

CREATE OR REPLACE FUNCTION enqueue_flight_search_event()
RETURNS trigger
LANGUAGE plpgsql
AS $$
BEGIN
    INSERT INTO flight_search_outbox (flight_id, operation, run_after, updated_at)
    VALUES (NEW.id, 'upsert', now(), now())
    ON CONFLICT (flight_id) DO UPDATE
       SET operation = 'upsert', run_after = now(), locked_by = NULL,
           locked_at = NULL, last_error = NULL, updated_at = now();
    RETURN NEW;
END;
$$;

DROP TRIGGER IF EXISTS flights_search_outbox_trigger ON flights;
CREATE TRIGGER flights_search_outbox_trigger
AFTER INSERT OR UPDATE ON flights
FOR EACH ROW EXECUTE FUNCTION enqueue_flight_search_event();

-- Existing data must also become searchable after the first deployment.
INSERT INTO flight_search_outbox (flight_id)
SELECT id FROM flights
ON CONFLICT (flight_id) DO NOTHING;
