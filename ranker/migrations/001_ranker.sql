CREATE TABLE IF NOT EXISTS flights (
    id BIGINT PRIMARY KEY,
    origin TEXT NOT NULL,
    destination TEXT NOT NULL,
    flight_date DATE NOT NULL,
    airline_code TEXT NOT NULL,
    airline_name TEXT NOT NULL DEFAULT '',
    flight_number TEXT NOT NULL,
    depart_at TIMESTAMPTZ NOT NULL,
    arrive_at TIMESTAMPTZ NOT NULL,
    depart_minute INT NOT NULL DEFAULT 0,
    duration_min INT,
    baggage_label TEXT NOT NULL DEFAULT '',
    baggage_kg INT,
    price_rial BIGINT NOT NULL DEFAULT 0,
    price_toman BIGINT NOT NULL DEFAULT 0,
    is_charter BOOLEAN NOT NULL DEFAULT FALSE,
    stops INT NOT NULL DEFAULT 0,
    source TEXT NOT NULL DEFAULT '',
    collected_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS idx_ranker_flights_route_date ON flights(origin, destination, flight_date);

CREATE TABLE IF NOT EXISTS ranked_flights (
    flight_id BIGINT PRIMARY KEY REFERENCES flights(id) ON DELETE CASCADE,
    origin TEXT NOT NULL,
    destination TEXT NOT NULL,
    flight_date DATE NOT NULL,
    rank INT NOT NULL,
    score DOUBLE PRECISION NOT NULL,
    ranked_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS idx_ranked_flights_route_date ON ranked_flights(origin, destination, flight_date, rank);

-- Backfill-compatible upgrades for databases created before arrival/duration
-- were mirrored from flight-service.
ALTER TABLE flights ADD COLUMN IF NOT EXISTS arrive_at TIMESTAMPTZ;
ALTER TABLE flights ADD COLUMN IF NOT EXISTS duration_min INT;
UPDATE flights SET arrive_at = depart_at WHERE arrive_at IS NULL;
