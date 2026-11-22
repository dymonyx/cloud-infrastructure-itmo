package main

import (
	"context"
	"database/sql"
	"embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"math/rand/v2"
	"net/http"
	"os"
	"strconv"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

//go:embed index.html
var staticFiles embed.FS

type trip struct {
	ID         string    `json:"id"`
	BikeID     string    `json:"bike_id"`
	DistanceKM float64   `json:"distance_km"`
	RouteName  string    `json:"route_name"`
	CreatedAt  time.Time `json:"created_at"`
}

type databaseState struct {
	Available bool   `json:"available"`
	Role      string `json:"role"`
	TripCount int64  `json:"trip_count"`
	Error     string `json:"error,omitempty"`
}

type statusResponse struct {
	WriteDB                 databaseState `json:"write_database"`
	ReadDB                  databaseState `json:"read_database"`
	MissingOnReadDB         int64         `json:"missing_on_read_database"`
	ReplicaLagBytes         float64       `json:"replica_lag_bytes"`
	ReplicaReplayLagSeconds float64       `json:"replica_replay_lag_seconds"`
	ReplicationConnectionUp bool          `json:"replication_connection_up"`
}

type metrics struct {
	writeDatabaseUp         prometheus.Gauge
	readDatabaseUp          prometheus.Gauge
	replicaLagBytes         prometheus.Gauge
	replicaReplayLagSeconds prometheus.Gauge
	replicationConnectionUp prometheus.Gauge
	primaryTripCount        prometheus.Gauge
	replicaTripCount        prometheus.Gauge
	missingTrips            prometheus.Gauge
	writeOperations         prometheus.Counter
	writeErrors             prometheus.Counter
}

type application struct {
	writeDB *sql.DB
	readDB  *sql.DB
	metrics metrics
}

func main() {
	writeDB := openDatabase(databaseURL("WRITE"))
	readDB := openDatabase(databaseURL("READ"))
	defer writeDB.Close()
	defer readDB.Close()

	app := &application{
		writeDB: writeDB,
		readDB:  readDB,
		metrics: newMetrics(),
	}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /", app.index)
	mux.HandleFunc("GET /healthz", app.health)
	mux.HandleFunc("GET /api/status", app.status)
	mux.HandleFunc("GET /api/trips", app.listTrips)
	mux.HandleFunc("POST /api/trips", app.createTrip)
	mux.HandleFunc("POST /api/load", app.createLoad)
	mux.Handle("GET /metrics", promhttp.Handler())

	go app.collectMetrics()

	port := env("HTTP_PORT", "8080")
	server := &http.Server{
		Addr:              ":" + port,
		Handler:           requestLogger(mux),
		ReadHeaderTimeout: 5 * time.Second,
		IdleTimeout:       30 * time.Second,
	}

	log.Printf("bike-api слушает порт %s", port)
	if err := server.ListenAndServe(); !errors.Is(err, http.ErrServerClosed) {
		log.Fatal(err)
	}
}

func openDatabase(url string) *sql.DB {
	db, err := sql.Open("pgx", url)
	if err != nil {
		log.Fatal(err)
	}
	db.SetMaxOpenConns(12)
	db.SetMaxIdleConns(4)
	db.SetConnMaxLifetime(5 * time.Minute)
	return db
}

func databaseURL(prefix string) string {
	host := env(prefix+"_DB_HOST", "localhost")
	port := env(prefix+"_DB_PORT", "5432")
	name := env("POSTGRES_DB", "chainwise")
	user := env("POSTGRES_APP_USER", "chainwise_app")
	password := env("POSTGRES_APP_PASSWORD", "chainwise-app-local-password")
	return fmt.Sprintf("postgres://%s:%s@%s:%s/%s?sslmode=disable", user, password, host, port, name)
}

func newMetrics() metrics {
	m := metrics{
		writeDatabaseUp: prometheus.NewGauge(prometheus.GaugeOpts{
			Name: "chainwise_postgres_write_database_up",
			Help: "Whether the database used for writes is available.",
		}),
		readDatabaseUp: prometheus.NewGauge(prometheus.GaugeOpts{
			Name: "chainwise_postgres_read_database_up",
			Help: "Whether the database used for reads is available.",
		}),
		replicaLagBytes: prometheus.NewGauge(prometheus.GaugeOpts{
			Name: "chainwise_postgres_replica_lag_bytes",
			Help: "Difference between the current primary WAL LSN and standby replay LSN.",
		}),
		replicaReplayLagSeconds: prometheus.NewGauge(prometheus.GaugeOpts{
			Name: "chainwise_postgres_replica_replay_lag_seconds",
			Help: "Replay lag reported by pg_stat_replication in seconds.",
		}),
		replicationConnectionUp: prometheus.NewGauge(prometheus.GaugeOpts{
			Name: "chainwise_postgres_replication_connection_up",
			Help: "Whether the primary sees an active streaming standby.",
		}),
		primaryTripCount: prometheus.NewGauge(prometheus.GaugeOpts{
			Name: "chainwise_postgres_primary_trip_count",
			Help: "Number of trips visible through the write connection.",
		}),
		replicaTripCount: prometheus.NewGauge(prometheus.GaugeOpts{
			Name: "chainwise_postgres_replica_trip_count",
			Help: "Number of trips visible through the read connection.",
		}),
		missingTrips: prometheus.NewGauge(prometheus.GaugeOpts{
			Name: "chainwise_postgres_missing_trips",
			Help: "Number of committed trips not yet visible through the read connection.",
		}),
		writeOperations: prometheus.NewCounter(prometheus.CounterOpts{
			Name: "chainwise_postgres_write_operations_total",
			Help: "Number of write operations requested by bike-api.",
		}),
		writeErrors: prometheus.NewCounter(prometheus.CounterOpts{
			Name: "chainwise_postgres_write_errors_total",
			Help: "Number of failed write operations in bike-api.",
		}),
	}
	prometheus.MustRegister(
		m.writeDatabaseUp,
		m.readDatabaseUp,
		m.replicaLagBytes,
		m.replicaReplayLagSeconds,
		m.replicationConnectionUp,
		m.primaryTripCount,
		m.replicaTripCount,
		m.missingTrips,
		m.writeOperations,
		m.writeErrors,
	)
	return m
}

func (a *application) index(w http.ResponseWriter, _ *http.Request) {
	page, err := staticFiles.ReadFile("index.html")
	if err != nil {
		http.Error(w, "страница не найдена", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = w.Write(page)
}

func (a *application) health(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
	defer cancel()
	if err := a.readDB.PingContext(ctx); err != nil {
		http.Error(w, "read database is unavailable", http.StatusServiceUnavailable)
		return
	}
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte("ok"))
}

func (a *application) status(w http.ResponseWriter, r *http.Request) {
	state := a.databaseStatus(r.Context())
	writeJSON(w, http.StatusOK, state)
}

func (a *application) listTrips(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 4*time.Second)
	defer cancel()

	rows, err := a.readDB.QueryContext(ctx, `
		SELECT id, bike_id, distance_km, route_name, created_at
		FROM trips
		ORDER BY created_at DESC
		LIMIT 50`)
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, "Не удалось прочитать поездки с реплики", err)
		return
	}
	defer rows.Close()

	trips := make([]trip, 0, 50)
	for rows.Next() {
		var item trip
		if err := rows.Scan(&item.ID, &item.BikeID, &item.DistanceKM, &item.RouteName, &item.CreatedAt); err != nil {
			writeError(w, http.StatusInternalServerError, "Не удалось разобрать данные поездки", err)
			return
		}
		trips = append(trips, item)
	}
	if err := rows.Err(); err != nil {
		writeError(w, http.StatusInternalServerError, "Чтение списка поездок завершилось с ошибкой", err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"records": trips, "source": "read database"})
}

func (a *application) createTrip(w http.ResponseWriter, r *http.Request) {
	a.metrics.writeOperations.Inc()
	ctx, cancel := context.WithTimeout(r.Context(), 4*time.Second)
	defer cancel()

	item := trip{
		ID:         newID("trip"),
		BikeID:     fmt.Sprintf("bike-demo-%03d", rand.IntN(20)+1),
		DistanceKM: float64(rand.IntN(480)+20) / 10,
		RouteName:  "Ручная поездка",
	}
	err := a.writeDB.QueryRowContext(ctx, `
		INSERT INTO trips (id, bike_id, distance_km, route_name)
		VALUES ($1, $2, $3, $4)
		RETURNING created_at`, item.ID, item.BikeID, item.DistanceKM, item.RouteName).Scan(&item.CreatedAt)
	if err != nil {
		a.metrics.writeErrors.Inc()
		writeError(w, http.StatusServiceUnavailable, "Запись в primary не прошла", err)
		return
	}
	writeJSON(w, http.StatusCreated, item)
}

func (a *application) createLoad(w http.ResponseWriter, r *http.Request) {
	count := 1000
	if raw := r.URL.Query().Get("count"); raw != "" {
		value, err := strconv.Atoi(raw)
		if err != nil || value < 1 || value > 10000 {
			writeError(w, http.StatusBadRequest, "Количество должно быть от 1 до 10000", nil)
			return
		}
		count = value
	}

	a.metrics.writeOperations.Inc()
	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()
	result, err := a.writeDB.ExecContext(ctx, `
		INSERT INTO trips (id, bike_id, distance_km, route_name)
		SELECT
			'trip-load-' || substr(md5(random()::text || clock_timestamp()::text || value::text), 1, 16),
			'bike-load-' || lpad(((value - 1) % 50 + 1)::text, 3, '0'),
			round((1 + random() * 99)::numeric, 1),
			'Нагрузочная поездка'
		FROM generate_series(1, $1) AS value`, count)
	if err != nil {
		a.metrics.writeErrors.Inc()
		writeError(w, http.StatusServiceUnavailable, "Нагрузочные записи не удалось добавить в primary", err)
		return
	}
	inserted, _ := result.RowsAffected()
	writeJSON(w, http.StatusCreated, map[string]any{"created": inserted})
}

func (a *application) databaseStatus(parent context.Context) statusResponse {
	ctx, cancel := context.WithTimeout(parent, 3*time.Second)
	defer cancel()

	writeState := inspectDatabase(ctx, a.writeDB)
	readState := inspectDatabase(ctx, a.readDB)
	response := statusResponse{WriteDB: writeState, ReadDB: readState}
	if writeState.Available && readState.Available && writeState.TripCount > readState.TripCount {
		response.MissingOnReadDB = writeState.TripCount - readState.TripCount
	}

	if writeState.Available && writeState.Role == "primary" {
		var lagBytes, lagSeconds float64
		err := a.writeDB.QueryRowContext(ctx, `
			SELECT
				COALESCE(pg_wal_lsn_diff(pg_current_wal_lsn(), replay_lsn), 0)::double precision,
				COALESCE(EXTRACT(EPOCH FROM replay_lag), 0)::double precision
			FROM pg_stat_replication
			WHERE application_name = 'chainwise-standby' AND state = 'streaming'
			LIMIT 1`).Scan(&lagBytes, &lagSeconds)
		if err == nil {
			response.ReplicaLagBytes = lagBytes
			if lagBytes > 0 {
				response.ReplicaReplayLagSeconds = lagSeconds
			}
			response.ReplicationConnectionUp = true
		}
	}
	return response
}

func inspectDatabase(ctx context.Context, db *sql.DB) databaseState {
	state := databaseState{}
	var inRecovery bool
	err := db.QueryRowContext(ctx, `SELECT pg_is_in_recovery(), (SELECT count(*) FROM trips)`).Scan(&inRecovery, &state.TripCount)
	if err != nil {
		state.Error = shortError(err)
		return state
	}
	state.Available = true
	state.Role = "primary"
	if inRecovery {
		state.Role = "standby"
	}
	return state
}

func (a *application) collectMetrics() {
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()
	for {
		state := a.databaseStatus(context.Background())
		setBool(a.metrics.writeDatabaseUp, state.WriteDB.Available)
		setBool(a.metrics.readDatabaseUp, state.ReadDB.Available)
		setBool(a.metrics.replicationConnectionUp, state.ReplicationConnectionUp)
		a.metrics.replicaLagBytes.Set(state.ReplicaLagBytes)
		a.metrics.replicaReplayLagSeconds.Set(state.ReplicaReplayLagSeconds)
		a.metrics.primaryTripCount.Set(float64(state.WriteDB.TripCount))
		a.metrics.replicaTripCount.Set(float64(state.ReadDB.TripCount))
		a.metrics.missingTrips.Set(float64(state.MissingOnReadDB))
		<-ticker.C
	}
}

func setBool(gauge prometheus.Gauge, value bool) {
	if value {
		gauge.Set(1)
		return
	}
	gauge.Set(0)
}

func newID(prefix string) string {
	data := make([]byte, 6)
	for i := range data {
		data[i] = byte(rand.IntN(256))
	}
	return prefix + "-" + hex.EncodeToString(data)
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(value); err != nil {
		log.Printf("не удалось отправить JSON: %v", err)
	}
}

func writeError(w http.ResponseWriter, status int, message string, err error) {
	if err != nil {
		log.Printf("%s: %v", message, err)
	}
	payload := map[string]string{"error": message}
	if err != nil {
		payload["details"] = shortError(err)
	}
	writeJSON(w, status, payload)
}

func shortError(err error) string {
	if err == nil {
		return ""
	}
	message := err.Error()
	if len(message) > 180 {
		return message[:180]
	}
	return message
}

func requestLogger(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		started := time.Now()
		next.ServeHTTP(w, r)
		log.Printf("%s %s %s", r.Method, r.URL.Path, time.Since(started).Round(time.Millisecond))
	})
}

func env(key, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
}
