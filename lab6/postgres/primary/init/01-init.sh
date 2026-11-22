#!/bin/sh
set -eu

psql --username "$POSTGRES_ADMIN_USER" --dbname "$POSTGRES_DB" \
  --set=app_user="$POSTGRES_APP_USER" \
  --set=app_password="$POSTGRES_APP_PASSWORD" \
  --set=replication_user="$POSTGRES_REPLICATION_USER" \
  --set=replication_password="$POSTGRES_REPLICATION_PASSWORD" \
  --set=exporter_user="$POSTGRES_EXPORTER_USER" \
  --set=exporter_password="$POSTGRES_EXPORTER_PASSWORD" <<'SQL'
SELECT format('CREATE ROLE %I LOGIN PASSWORD %L', :'app_user', :'app_password')
WHERE NOT EXISTS (SELECT FROM pg_roles WHERE rolname = :'app_user') \gexec

SELECT format('CREATE ROLE %I WITH REPLICATION LOGIN PASSWORD %L', :'replication_user', :'replication_password')
WHERE NOT EXISTS (SELECT FROM pg_roles WHERE rolname = :'replication_user') \gexec

SELECT format('CREATE ROLE %I LOGIN PASSWORD %L', :'exporter_user', :'exporter_password')
WHERE NOT EXISTS (SELECT FROM pg_roles WHERE rolname = :'exporter_user') \gexec

SELECT format('GRANT CONNECT ON DATABASE %I TO %I', current_database(), :'app_user') \gexec
SELECT format('GRANT CONNECT ON DATABASE %I TO %I', current_database(), :'exporter_user') \gexec
SELECT format('GRANT pg_monitor TO %I', :'exporter_user') \gexec
SELECT format('GRANT pg_monitor TO %I', :'app_user') \gexec

SELECT format('GRANT USAGE, CREATE ON SCHEMA public TO %I', :'app_user') \gexec

CREATE TABLE IF NOT EXISTS trips (
    id text PRIMARY KEY,
    bike_id text NOT NULL,
    distance_km numeric(8, 1) NOT NULL CHECK (distance_km > 0),
    route_name text NOT NULL,
    created_at timestamptz NOT NULL DEFAULT clock_timestamp()
);

CREATE INDEX IF NOT EXISTS trips_created_at_idx ON trips (created_at DESC);

SELECT format('ALTER TABLE trips OWNER TO %I', :'app_user') \gexec
SELECT format('ALTER INDEX trips_created_at_idx OWNER TO %I', :'app_user') \gexec
SELECT format('GRANT SELECT ON ALL TABLES IN SCHEMA public TO %I', :'exporter_user') \gexec

SELECT pg_create_physical_replication_slot('chainwise_standby_slot')
WHERE NOT EXISTS (
    SELECT FROM pg_replication_slots WHERE slot_name = 'chainwise_standby_slot'
);
SQL

cat >> "$PGDATA/pg_hba.conf" <<EOF
host replication $POSTGRES_REPLICATION_USER 0.0.0.0/0 scram-sha-256
host $POSTGRES_DB $POSTGRES_APP_USER 0.0.0.0/0 scram-sha-256
host $POSTGRES_DB $POSTGRES_EXPORTER_USER 0.0.0.0/0 scram-sha-256
EOF
