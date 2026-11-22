#!/bin/sh
set -eu

if [ "$(id -u)" = "0" ]; then
  mkdir -p "$PGDATA"
  chown -R postgres:postgres "$PGDATA"
  exec gosu postgres "$0" "$@"
fi

if [ ! -s "$PGDATA/PG_VERSION" ]; then
  echo "Ожидаю primary перед созданием standby"
  until pg_isready -h "$PRIMARY_HOST" -p "$PRIMARY_PORT" -U "$POSTGRES_ADMIN_USER" -d "$POSTGRES_DB" >/dev/null 2>&1; do
    sleep 2
  done

  find "$PGDATA" -mindepth 1 -maxdepth 1 -exec rm -rf {} +

  export PGPASSWORD="$POSTGRES_REPLICATION_PASSWORD"
  pg_basebackup \
    --host="$PRIMARY_HOST" \
    --port="$PRIMARY_PORT" \
    --username="$POSTGRES_REPLICATION_USER" \
    --pgdata="$PGDATA" \
    --format=plain \
    --wal-method=stream \
    --checkpoint=fast \
    --slot=chainwise_standby_slot \
    --progress
  unset PGPASSWORD

  printf '%s:%s:*:%s:%s\n' \
    "$PRIMARY_HOST" \
    "$PRIMARY_PORT" \
    "$POSTGRES_REPLICATION_USER" \
    "$POSTGRES_REPLICATION_PASSWORD" > "$PGDATA/.pgpass"
  chmod 600 "$PGDATA/.pgpass"

  touch "$PGDATA/standby.signal"
  cat >> "$PGDATA/postgresql.auto.conf" <<EOF
primary_conninfo = 'host=$PRIMARY_HOST port=$PRIMARY_PORT user=$POSTGRES_REPLICATION_USER passfile=$PGDATA/.pgpass application_name=chainwise-standby'
primary_slot_name = 'chainwise_standby_slot'
recovery_target_timeline = 'latest'
hot_standby = 'on'
EOF
  chmod 700 "$PGDATA"
fi

exec docker-entrypoint.sh "$@"
