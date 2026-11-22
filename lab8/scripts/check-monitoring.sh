#!/bin/sh
set -eu

echo "Elasticsearch exporter"
curl --fail --silent http://localhost:9114/metrics \
  | grep -E '^(elasticsearch_cluster_health_status|elasticsearch_jvm_memory_used_bytes|elasticsearch_filesystem_data_available_bytes|elasticsearch_indices_docs_primary)'

echo
echo "Prometheus target"
curl --fail --silent 'http://localhost:9090/api/v1/targets?state=active' \
  | jq '.data.activeTargets[] | {job: .labels.job, health: .health, lastError: .lastError}'

echo
echo "Правила алертов"
curl --fail --silent http://localhost:9090/api/v1/rules \
  | jq '.data.groups[] | select(.name == "chainwise-elasticsearch") | .rules[] | {name, state, health}'

