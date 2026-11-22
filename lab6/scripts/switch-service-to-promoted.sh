#!/bin/sh
set -eu

LAB_DIR=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
cd "$LAB_DIR"

docker compose \
  --file docker-compose.yml \
  --file docker-compose.failover.yml \
  up --detach --no-deps --force-recreate bike-api

echo "bike-api переключён на повышенный postgres-standby"
echo "Write и read подключения теперь смотрят на новый primary"

