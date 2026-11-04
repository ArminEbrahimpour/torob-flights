#!/usr/bin/env sh
# Reset the application user on every broker start. RabbitMQ ignores
# RABBITMQ_DEFAULT_USER / RABBITMQ_DEFAULT_PASS once the data volume exists,
# which otherwise leaves clients with "username or password not allowed".
set -eu

user="${RABBITMQ_DEFAULT_USER:-${RABBITMQ_USER:-torob}}"
pass="${RABBITMQ_DEFAULT_PASS:-${RABBITMQ_PASSWORD:-torob}}"

if [ -z "$user" ] || [ -z "$pass" ]; then
  echo "RabbitMQ application user and password are required" >&2
  exit 1
fi

# The official entrypoint starts as root, then re-execs as rabbitmq.
# rabbitmqctl only works with that user's Erlang cookie.
as_rabbit() {
  if [ "$(id -u)" = 0 ] && command -v su-exec >/dev/null 2>&1; then
    su-exec rabbitmq "$@"
  elif [ "$(id -u)" = 0 ] && command -v gosu >/dev/null 2>&1; then
    gosu rabbitmq "$@"
  else
    "$@"
  fi
}

until as_rabbit rabbitmq-diagnostics -q ping >/dev/null 2>&1; do
  echo "waiting for RabbitMQ broker"
  sleep 2
done

if as_rabbit rabbitmqctl list_users | awk '{print $1}' | grep -Fx "$user" >/dev/null; then
  as_rabbit rabbitmqctl change_password "$user" "$pass"
else
  as_rabbit rabbitmqctl add_user "$user" "$pass"
fi
as_rabbit rabbitmqctl set_user_tags "$user" administrator
as_rabbit rabbitmqctl set_permissions -p / "$user" ".*" ".*" ".*"

# guest is loopback-only. Clients on the Docker network that still use it get
# a 403 ("username or password not allowed") instead of a useful auth error.
if [ "$user" != "guest" ] && as_rabbit rabbitmqctl list_users | awk '{print $1}' | grep -Fx guest >/dev/null; then
  as_rabbit rabbitmqctl delete_user guest
fi

echo "RabbitMQ application credentials reconciled for user ${user}"
