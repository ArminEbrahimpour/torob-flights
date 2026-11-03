-- Preserve every provider offer, including offers with identical schedule and price
-- but different baggage, fare rules, or proposal identifiers.
DROP INDEX IF EXISTS uq_flights_natural;
CREATE UNIQUE INDEX IF NOT EXISTS uq_flights_natural
    ON flights (
        source, origin, destination, flight_date,
        airline_code, flight_number,
        depart_at, arrive_at, price_rial, is_charter, proposal_id
    );

CREATE INDEX IF NOT EXISTS idx_flights_proposal
    ON flights (source, proposal_id) WHERE proposal_id <> '';
