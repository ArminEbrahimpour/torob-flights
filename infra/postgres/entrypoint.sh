#!/usr/bin/env sh
set -eu
trap '' HUP
(
  trap '' HUP
  until /usr/local/bin/reconcile-roles; do
    echo "PostgreSQL credential reconcile failed; retrying" >&2
    sleep 2
  done
) &
exec docker-entrypoint.sh "$@"
