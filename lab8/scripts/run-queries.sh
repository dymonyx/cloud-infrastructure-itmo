#!/bin/sh
set -eu

ES_URL=${ES_URL:-http://localhost:9200}

query() {
  title=$1
  body=$2
  echo
  echo "$title"
  curl --fail --silent --show-error \
    --request GET "$ES_URL/chainwise-logs/_search" \
    --header 'Content-Type: application/json' \
    --data-binary "$body" \
    | jq .
}

query "Полнотекстовый поиск по message" '{
  "size": 20,
  "query": {"match": {"message": "цепи"}},
  "sort": [{"timestamp": "asc"}]
}'

query "ERROR за выбранный диапазон" '{
  "size": 20,
  "query": {
    "bool": {
      "filter": [
        {"term": {"level": "ERROR"}},
        {"range": {"timestamp": {"gte": "2026-11-22T10:00:00Z", "lte": "2026-11-22T10:59:59Z"}}}
      ]
    }
  },
  "sort": [{"timestamp": "asc"}]
}'

query "Поиск по trace_id" '{
  "size": 20,
  "query": {"term": {"trace_id": "trace-ride-1007"}},
  "sort": [{"timestamp": "asc"}]
}'

query "Агрегация по level" '{
  "size": 0,
  "aggs": {
    "logs_by_level": {
      "terms": {"field": "level", "size": 10}
    }
  }
}'

