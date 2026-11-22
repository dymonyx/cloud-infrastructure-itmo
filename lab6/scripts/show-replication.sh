#!/bin/sh
set -eu

LAB_DIR=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
cd "$LAB_DIR"

echo "PRIMARY"
if docker compose exec -T postgres-primary sh -c '
  psql --username "$POSTGRES_ADMIN_USER" --dbname "$POSTGRES_DB" --no-psqlrc --command "
    SELECT application_name,
           client_addr,
           state,
           sync_state,
           sent_lsn,
           write_lsn,
           flush_lsn,
           replay_lsn,
           pg_size_pretty(pg_wal_lsn_diff(pg_current_wal_lsn(), replay_lsn)) AS lag
    FROM pg_stat_replication;
  "
'; then
  :
else
  echo "Primary сейчас недоступен"
fi

echo
echo "STANDBY"
docker compose exec -T postgres-standby sh -c '
  psql --username "$POSTGRES_ADMIN_USER" --dbname "$POSTGRES_DB" --no-psqlrc --command "
    SELECT pg_is_in_recovery() AS is_standby,
           pg_is_wal_replay_paused() AS replay_paused,
           pg_last_wal_receive_lsn() AS received_lsn,
           pg_last_wal_replay_lsn() AS replayed_lsn,
           pg_last_xact_replay_timestamp() AS last_replay_timestamp;
  "
'
