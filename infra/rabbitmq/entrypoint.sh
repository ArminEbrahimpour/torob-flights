#!/usr/bin/env sh
# Keep reconciling across exec of the official entrypoint. A failed attempt
# (broker still booting, cookie not ready) is retried until the password
# stored in the volume matches the container environment.
set -eu
trap '' HUP
(
  trap '' HUP
  until /usr/local/bin/reconcile-user; do
    echo "RabbitMQ credential reconcile failed; retrying" >&2
    sleep 2
  done
) &
exec docker-entrypoint.sh "$@"
