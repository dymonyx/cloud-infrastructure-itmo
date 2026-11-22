#!/bin/sh
set -eu

LAB_DIR=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
cd "$LAB_DIR"

echo "Проверка роли write database"
docker compose exec -T postgres-primary sh -c '
  psql --username "$POSTGRES_ADMIN_USER" --dbname "$POSTGRES_DB" --no-psqlrc --command "SELECT pg_is_in_recovery() AS is_standby, (SELECT count(*) FROM trips) AS trips;"
'

echo
echo "Проверка роли read database и запрета записи"
docker compose exec -T postgres-standby sh -c '
  psql --username "$POSTGRES_ADMIN_USER" --dbname "$POSTGRES_DB" --no-psqlrc --command "SELECT pg_is_in_recovery() AS is_standby, current_setting('"'"'transaction_read_only'"'"') AS read_only, (SELECT count(*) FROM trips) AS trips;"
'

echo
echo "Состояние streaming replication"
"$LAB_DIR/scripts/show-replication.sh"
