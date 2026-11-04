#!/usr/bin/env sh
set -eu

as_rabbit() {
  if [ "$(id -u)" = 0 ] && command -v su-exec >/dev/null 2>&1; then
    su-exec rabbitmq "$@"
  elif [ "$(id -u)" = 0 ] && command -v gosu >/dev/null 2>&1; then
    gosu rabbitmq "$@"
  else
    "$@"
  fi
}

as_rabbit rabbitmq-diagnostics -q ping
as_rabbit rabbitmqctl authenticate_user "${RABBITMQ_DEFAULT_USER:-torob}" "${RABBITMQ_DEFAULT_PASS:-torob}"
