#!/usr/bin/env sh
set -eu

network_name="${TOROB_NETWORK_NAME:-torob-net}"
if ! docker network inspect "$network_name" >/dev/null 2>&1; then
  docker network create "$network_name" >/dev/null
  echo "Created Docker network: $network_name"
fi

# Recreate the stateful services so the credential entrypoints are the
# running process. Volumes are kept; passwords are rewritten in place.
docker compose up -d --force-recreate postgres rabbitmq
docker compose up -d "$@"
