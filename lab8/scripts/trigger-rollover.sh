#!/bin/sh
set -eu

LAB_DIR=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
ES_URL=${ES_URL:-http://localhost:9200}

for command in curl jq; do
  if ! command -v "$command" >/dev/null 2>&1; then
    echo "Для запуска нужен $command"
    exit 1
  fi
done

total_count=$(curl --fail --silent "$ES_URL/chainwise-logs-*/_count" | jq -r '.count')
if [ "$total_count" -lt 60 ]; then
  echo "Добавляю ещё 20 логов через alias chainwise-logs"
  bulk_result=$(curl --fail --silent --show-error \
    --request POST "$ES_URL/chainwise-logs/_bulk?refresh=true" \
    --header 'Content-Type: application/x-ndjson' \
    --data-binary "@$LAB_DIR/data/rollover-logs.ndjson")
  if [ "$(printf '%s' "$bulk_result" | jq -r '.errors')" != "false" ]; then
    printf '%s\n' "$bulk_result" | jq .
    echo "Часть документов не загрузилась"
    exit 1
  fi
else
  echo "Дополнительные логи уже загружены"
fi

echo "Ожидаю автоматический rollover от ILM"
attempt=0
while ! curl --fail --silent "$ES_URL/_cat/indices/chainwise-logs-000002?h=index" | grep -q 'chainwise-logs-000002'; do
  attempt=$((attempt + 1))
  if [ "$attempt" -ge 24 ]; then
    echo "Новый индекс не появился за две минуты"
    echo "Проверь состояние через GET chainwise-logs-*/_ilm/explain"
    exit 1
  fi
  sleep 5
done

echo
echo "Индексы после rollover"
curl --fail --silent "$ES_URL/_cat/indices/chainwise-logs-*?v&s=index"

echo
echo "Alias после rollover"
curl --fail --silent "$ES_URL/_cat/aliases/chainwise-logs?v"

echo
echo "Состояние ILM"
curl --fail --silent "$ES_URL/chainwise-logs-*/_ilm/explain?pretty"

