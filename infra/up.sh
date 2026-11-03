#!/usr/bin/env sh
set -eu

network_name="${TOROB_NETWORK_NAME:-torob-net}"
if ! docker network inspect "$network_name" >/dev/null 2>&1; then
  docker network create "$network_name" >/dev/null
  echo "Created Docker network: $network_name"
fi

docker compose up -d "$@"
