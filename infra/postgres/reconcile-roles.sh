#!/usr/bin/env sh
# Apply the superuser and application passwords on every start. Init scripts
# run only when the data volume is created, so a later env change would
# otherwise leave clients with authentication failures.
set -eu

db_user="${POSTGRES_SUPERUSER:-${POSTGRES_USER:-postgres}}"
db_password="${POSTGRES_SUPERUSER_PASSWORD:-${POSTGRES_PASSWORD:-}}"
flight_user="${FLIGHT_DB_USER:-flight}"
flight_password="${FLIGHT_DB_PASSWORD:-flight}"
flight_database="${FLIGHT_DB_NAME:-flight_services}"
ranker_user="${RANKER_DB_USER:-ranker}"
ranker_password="${RANKER_DB_PASSWORD:-ranker}"
ranker_database="${RANKER_DB_NAME:-ranker_db}"

if [ -z "$db_password" ]; then
  echo "PostgreSQL superuser password is empty" >&2
  exit 1
fi

until pg_isready -U "$db_user" -d postgres >/dev/null 2>&1; do
  echo "waiting for PostgreSQL"
  sleep 2
done

# Local socket is trust (see pg_hba.conf). Do not pass PGPASSWORD here:
# the stored superuser password may still be the old one.
psql -v ON_ERROR_STOP=1 -U "$db_user" -d postgres \
  -v super_user="$db_user" -v super_password="$db_password" \
  -v flight_user="$flight_user" -v flight_password="$flight_password" -v flight_database="$flight_database" \
  -v ranker_user="$ranker_user" -v ranker_password="$ranker_password" -v ranker_database="$ranker_database" <<'SQL'
SELECT format('ALTER ROLE %I LOGIN PASSWORD %L', :'super_user', :'super_password')\gexec
SELECT format('CREATE ROLE %I LOGIN PASSWORD %L', :'flight_user', :'flight_password') WHERE NOT EXISTS (SELECT FROM pg_roles WHERE rolname = :'flight_user')\gexec
SELECT format('ALTER ROLE %I LOGIN PASSWORD %L', :'flight_user', :'flight_password')\gexec
SELECT format('CREATE DATABASE %I OWNER %I', :'flight_database', :'flight_user') WHERE NOT EXISTS (SELECT FROM pg_database WHERE datname = :'flight_database')\gexec
SELECT format('CREATE ROLE %I LOGIN PASSWORD %L', :'ranker_user', :'ranker_password') WHERE NOT EXISTS (SELECT FROM pg_roles WHERE rolname = :'ranker_user')\gexec
SELECT format('ALTER ROLE %I LOGIN PASSWORD %L', :'ranker_user', :'ranker_password')\gexec
SELECT format('CREATE DATABASE %I OWNER %I', :'ranker_database', :'ranker_user') WHERE NOT EXISTS (SELECT FROM pg_database WHERE datname = :'ranker_database')\gexec
SQL

echo "PostgreSQL application credentials reconciled"
