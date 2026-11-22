#!/bin/sh
set -eu

LAB_DIR=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
cd "$LAB_DIR"

ROLE=$(docker compose exec -T postgres-standby sh -c '
  psql --username "$POSTGRES_ADMIN_USER" --dbname "$POSTGRES_DB" --no-psqlrc --tuples-only --no-align --command "SELECT pg_is_in_recovery();"
' | tr -d '[:space:]')

if [ "$ROLE" = "f" ]; then
  echo "Standby уже повышен до primary"
  exit 0
fi

docker compose exec -T postgres-standby sh -c '
  psql --username "$POSTGRES_ADMIN_USER" --dbname "$POSTGRES_DB" --no-psqlrc --set=ON_ERROR_STOP=1 --command "SELECT pg_promote(true, 60);"
'

docker compose exec -T postgres-standby sh -c '
  psql --username "$POSTGRES_ADMIN_USER" --dbname "$POSTGRES_DB" --no-psqlrc --command "SELECT pg_is_in_recovery() AS is_standby;"
'

echo "Узел postgres-standby теперь работает как primary"
echo "Старый primary не запускай, пока его данные не будут пересозданы от нового primary"

