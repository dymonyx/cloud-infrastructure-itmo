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

echo "Ожидаю Elasticsearch"
attempt=0
until curl --fail --silent "$ES_URL" >/dev/null 2>&1; do
  attempt=$((attempt + 1))
  if [ "$attempt" -ge 60 ]; then
    echo "Elasticsearch не запустился за две минуты"
    exit 1
  fi
  sleep 2
done

curl --fail --silent --show-error \
  --request PUT "$ES_URL/_cluster/settings" \
  --header 'Content-Type: application/json' \
  --data-binary '{"persistent":{"indices.lifecycle.poll_interval":"5s"}}' \
  | jq .

curl --fail --silent --show-error \
  --request PUT "$ES_URL/_ilm/policy/chainwise-logs-policy" \
  --header 'Content-Type: application/json' \
  --data-binary "@$LAB_DIR/config/ilm-policy.json" \
  | jq .

curl --fail --silent --show-error \
  --request PUT "$ES_URL/_index_template/chainwise-logs-template" \
  --header 'Content-Type: application/json' \
  --data-binary "@$LAB_DIR/config/index-template.json" \
  | jq .

if ! curl --fail --silent "$ES_URL/_alias/chainwise-logs" >/dev/null 2>&1; then
  curl --fail --silent --show-error \
    --request PUT "$ES_URL/chainwise-logs-000001" \
    --header 'Content-Type: application/json' \
    --data-binary '{"aliases":{"chainwise-logs":{"is_write_index":true}}}' \
    | jq .
fi

document_count=$(curl --fail --silent "$ES_URL/chainwise-logs-*/_count" | jq -r '.count')
if [ "$document_count" -eq 0 ]; then
  bulk_result=$(curl --fail --silent --show-error \
    --request POST "$ES_URL/chainwise-logs/_bulk?refresh=true" \
    --header 'Content-Type: application/x-ndjson' \
    --data-binary "@$LAB_DIR/data/chainwise-logs.ndjson")
  if [ "$(printf '%s' "$bulk_result" | jq -r '.errors')" != "false" ]; then
    printf '%s\n' "$bulk_result" | jq .
    echo "Часть документов не загрузилась"
    exit 1
  fi
fi

echo
echo "Индексы и количество документов"
curl --fail --silent "$ES_URL/_cat/indices/chainwise-logs-*?v&s=index"

echo
echo "Alias для записи"
curl --fail --silent "$ES_URL/_cat/aliases/chainwise-logs?v"

echo
echo "Состояние кластера"
curl --fail --silent "$ES_URL/_cluster/health?pretty"

