-- Enable multiple collectors (Alibaba, Tahagasht, future providers) to share
-- the same PostgreSQL queue and flights table without stealing/overwriting
-- each other's work.

-- ---------------------------------------------------------------------------
-- Provider-scoped job queue
-- ---------------------------------------------------------------------------
ALTER TABLE jobs
    ADD COLUMN IF NOT EXISTS source TEXT NOT NULL DEFAULT 'alibaba';

-- Existing rows came from the Alibaba-only collector.
UPDATE jobs SET source = 'alibaba' WHERE source IS NULL OR source = '';

-- The old index allowed only one active collect_day per route/date globally.
DROP INDEX IF EXISTS uq_jobs_collect_day_active;
CREATE UNIQUE INDEX IF NOT EXISTS uq_jobs_collect_day_active
    ON jobs (source, origin, destination, flight_date)
    WHERE kind = 'collect_day' AND status IN ('pending', 'running');

CREATE INDEX IF NOT EXISTS idx_jobs_claim_source
    ON jobs (source, status, run_after, priority, id)
    WHERE status = 'pending';

-- route.last_enqueued_at is global and therefore cannot represent two
-- collectors with independent refresh schedules. Keep provider state here.
CREATE TABLE IF NOT EXISTS collector_route_state (
    source           TEXT NOT NULL,
    route_id         BIGINT NOT NULL REFERENCES routes(id) ON DELETE CASCADE,
    last_enqueued_at TIMESTAMPTZ,
    PRIMARY KEY (source, route_id)
);

INSERT INTO collector_route_state (source, route_id, last_enqueued_at)
SELECT 'alibaba', id, last_enqueued_at
FROM routes
WHERE last_enqueued_at IS NOT NULL
ON CONFLICT (source, route_id) DO NOTHING;

-- ---------------------------------------------------------------------------
-- Provider-scoped flight snapshots
-- ---------------------------------------------------------------------------
-- The original natural key omitted source, so an identical Tahagasht row
-- could update an Alibaba row (including changing its source). Preserve both.
DROP INDEX IF EXISTS uq_flights_natural;
CREATE UNIQUE INDEX IF NOT EXISTS uq_flights_natural
    ON flights (
        source, origin, destination, flight_date,
        airline_code, flight_number,
        depart_at, arrive_at, price_rial, is_charter
    );

CREATE INDEX IF NOT EXISTS idx_flights_source_od_date
    ON flights (source, origin, destination, flight_date);

-- ---------------------------------------------------------------------------
-- Observability
-- ---------------------------------------------------------------------------
ALTER TABLE collection_runs
    ADD COLUMN IF NOT EXISTS source TEXT NOT NULL DEFAULT 'alibaba';

UPDATE collection_runs SET source = 'alibaba' WHERE source IS NULL OR source = '';

CREATE INDEX IF NOT EXISTS idx_runs_source_od_date
    ON collection_runs (source, origin, destination, flight_date, created_at DESC);
