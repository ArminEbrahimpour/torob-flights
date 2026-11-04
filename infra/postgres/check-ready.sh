#!/usr/bin/env sh
set -eu
pg_isready -U "${POSTGRES_USER:-postgres}" -d postgres
PGPASSWORD="${POSTGRES_PASSWORD}" psql -h 127.0.0.1 -U "${POSTGRES_USER:-postgres}" -d postgres -c 'SELECT 1' >/dev/null
PGPASSWORD="${FLIGHT_DB_PASSWORD:-flight}" psql -h 127.0.0.1 -U "${FLIGHT_DB_USER:-flight}" -d "${FLIGHT_DB_NAME:-flight_services}" -c 'SELECT 1' >/dev/null
PGPASSWORD="${RANKER_DB_PASSWORD:-ranker}" psql -h 127.0.0.1 -U "${RANKER_DB_USER:-ranker}" -d "${RANKER_DB_NAME:-ranker_db}" -c 'SELECT 1' >/dev/null
