#!/bin/sh
set -eu

LAB_DIR=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
cd "$LAB_DIR"

docker compose exec -T postgres-standby sh -c '
  psql --username "$POSTGRES_ADMIN_USER" --dbname "$POSTGRES_DB" --no-psqlrc --set=ON_ERROR_STOP=1 --command "SELECT pg_wal_replay_resume();"
'

echo "Применение WAL на standby продолжено"

