#!/bin/sh
set -eu

ES_URL=${ES_URL:-http://localhost:9200}

echo "Состояние кластера"
curl --fail --silent "$ES_URL/_cluster/health?pretty"

echo
echo "Распределение shards"
curl --fail --silent "$ES_URL/_cat/shards/chainwise-logs-*?v&s=index,shard,prirep"

