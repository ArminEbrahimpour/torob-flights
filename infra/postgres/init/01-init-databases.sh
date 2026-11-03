#!/usr/bin/env bash
set -euo pipefail

create_role_and_database() {
  local role="$1" password="$2" database="$3"
  psql -v ON_ERROR_STOP=1 --username "$POSTGRES_USER" --dbname postgres \
    --variable=role="$role" --variable=password="$password" --variable=database="$database" <<'SQL'
SELECT format('CREATE ROLE %I LOGIN PASSWORD %L', :'role', :'password')
WHERE NOT EXISTS (SELECT FROM pg_roles WHERE rolname = :'role')\gexec
SELECT format('CREATE DATABASE %I OWNER %I', :'database', :'role')
WHERE NOT EXISTS (SELECT FROM pg_database WHERE datname = :'database')\gexec
SQL
}

create_role_and_database "${FLIGHT_DB_USER}" "${FLIGHT_DB_PASSWORD}" "${FLIGHT_DB_NAME}"
create_role_and_database "${RANKER_DB_USER}" "${RANKER_DB_PASSWORD}" "${RANKER_DB_NAME}"
