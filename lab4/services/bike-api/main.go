package main

import (
	"context"
	"crypto/rand"
	"embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/IBM/sarama"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

//go:embed index.html
var assets embed.FS

type tripRecorded struct {
	EventType  string    `json:"event_type"`
	TripID     string    `json:"trip_id"`
	BikeID     string    `json:"bike_id"`
	DistanceKM float64   `json:"distance_km"`
	RecordedAt time.Time `json:"recorded_at"`
}

type publishedTrip struct {
	tripRecorded
	Partition int32 `json:"partition"`
	Offset    int64 `json:"offset"`
}

type application struct {
	logger    *slog.Logger
	producer  sarama.SyncProducer
	topic     string
	published prometheus.Counter
	failures  prometheus.Counter
	mu        sync.RWMutex
	history   []publishedTrip
}

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil)).With("service", "bike-api")
	brokers := splitCSV(env("KAFKA_BROKERS", "localhost:9092"))
	topic := env("KAFKA_TOPIC", "trip-recorded")
	port := env("PORT", "8081")

	producer, err := newProducerWithRetry(brokers, 60*time.Second)
	if err != nil {
		logger.Error("cannot connect to Kafka", "error", err)
		os.Exit(1)
	}
	defer producer.Close()

	registry := prometheus.NewRegistry()
	published := prometheus.NewCounter(prometheus.CounterOpts{
		Name: "chainwise_trips_published_total",
		Help: "Number of trip-recorded events successfully published to Kafka.",
	})
	failures := prometheus.NewCounter(prometheus.CounterOpts{
		Name: "chainwise_trip_publish_failures_total",
		Help: "Number of trip-recorded events that failed to publish to Kafka.",
	})
	registry.MustRegister(published, failures, prometheus.NewGoCollector(), prometheus.NewProcessCollector(prometheus.ProcessCollectorOpts{}))

	app := &application{
		logger:    logger,
		producer:  producer,
		topic:     topic,
		published: published,
		failures:  failures,
	}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /", serveIndex)
	mux.HandleFunc("POST /api/trips", app.createTrip)
	mux.HandleFunc("GET /api/trips", app.listTrips)
	mux.HandleFunc("GET /healthz", health)
	mux.HandleFunc("GET /readyz", health)
	mux.Handle("GET /metrics", promhttp.HandlerFor(registry, promhttp.HandlerOpts{}))

	server := &http.Server{
		Addr:              ":" + port,
		Handler:           accessLog(logger, mux),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       10 * time.Second,
		WriteTimeout:      10 * time.Second,
		IdleTimeout:       60 * time.Second,
	}

	errCh := make(chan error, 1)
	go func() {
		logger.Info("bike-api started", "port", port, "topic", topic, "brokers", brokers)
		if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- err
		}
	}()

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, syscall.SIGINT, syscall.SIGTERM)
	select {
	case sig := <-stop:
		logger.Info("shutdown signal received", "signal", sig.String())
	case err := <-errCh:
		logger.Error("HTTP server failed", "error", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	_ = server.Shutdown(ctx)
}

func (app *application) createTrip(w http.ResponseWriter, r *http.Request) {
	var input struct {
		BikeID     string  `json:"bike_id"`
		DistanceKM float64 `json:"distance_km"`
	}
	decoder := json.NewDecoder(io.LimitReader(r.Body, 1<<20))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&input); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	input.BikeID = strings.TrimSpace(input.BikeID)
	if input.BikeID == "" || input.DistanceKM <= 0 || input.DistanceKM > 1000 {
		writeError(w, http.StatusBadRequest, "bike_id is required and distance_km must be between 0 and 1000")
		return
	}

	event := tripRecorded{
		EventType:  "trip-recorded",
		TripID:     newID("trip"),
		BikeID:     input.BikeID,
		DistanceKM: input.DistanceKM,
		RecordedAt: time.Now().UTC(),
	}
	payload, err := json.Marshal(event)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "cannot encode event")
		return
	}

	partition, offset, err := app.producer.SendMessage(&sarama.ProducerMessage{
		Topic: app.topic,
		Value: sarama.ByteEncoder(payload),
		Headers: []sarama.RecordHeader{
			{Key: []byte("event-type"), Value: []byte(event.EventType)},
		},
	})
	if err != nil {
		app.failures.Inc()
		app.logger.Error("event publish failed", "trip_id", event.TripID, "error", err)
		writeError(w, http.StatusServiceUnavailable, "Kafka publish failed")
		return
	}

	result := publishedTrip{tripRecorded: event, Partition: partition, Offset: offset}
	app.mu.Lock()
	app.history = append([]publishedTrip{result}, app.history...)
	if len(app.history) > 50 {
		app.history = app.history[:50]
	}
	app.mu.Unlock()
	app.published.Inc()
	app.logger.Info("trip-recorded published", "trip_id", event.TripID, "partition", partition, "offset", offset)
	writeJSON(w, http.StatusCreated, result)
}

func (app *application) listTrips(w http.ResponseWriter, _ *http.Request) {
	app.mu.RLock()
	items := append([]publishedTrip(nil), app.history...)
	app.mu.RUnlock()
	writeJSON(w, http.StatusOK, map[string]any{"topic": app.topic, "items": items})
}

func serveIndex(w http.ResponseWriter, _ *http.Request) {
	page, err := assets.ReadFile("index.html")
	if err != nil {
		http.Error(w, "page unavailable", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = w.Write(page)
}

func health(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"service": "bike-api", "status": "ok"})
}

func newProducerWithRetry(brokers []string, timeout time.Duration) (sarama.SyncProducer, error) {
	deadline := time.Now().Add(timeout)
	var lastErr error
	for time.Now().Before(deadline) {
		config := sarama.NewConfig()
		config.ClientID = "chainwise-bike-api"
		config.Producer.RequiredAcks = sarama.WaitForAll
		config.Producer.Return.Successes = true
		config.Producer.Retry.Max = 10
		config.Producer.Partitioner = sarama.NewRoundRobinPartitioner
		producer, err := sarama.NewSyncProducer(brokers, config)
		if err == nil {
			return producer, nil
		}
		lastErr = err
		time.Sleep(2 * time.Second)
	}
	return nil, fmt.Errorf("Kafka unavailable after %s: %w", timeout, lastErr)
}

func newID(prefix string) string {
	bytes := make([]byte, 6)
	if _, err := rand.Read(bytes); err != nil {
		return prefix + "-" + strconv.FormatInt(time.Now().UnixNano(), 10)
	}
	return prefix + "-" + hex.EncodeToString(bytes)
}

func accessLog(logger *slog.Logger, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		started := time.Now()
		next.ServeHTTP(w, r)
		logger.Info("HTTP request", "method", r.Method, "path", r.URL.Path, "duration_ms", time.Since(started).Milliseconds())
	})
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func writeError(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, map[string]string{"error": message})
}

func splitCSV(value string) []string {
	parts := strings.Split(value, ",")
	result := make([]string, 0, len(parts))
	for _, part := range parts {
		if trimmed := strings.TrimSpace(part); trimmed != "" {
			result = append(result, trimmed)
		}
	}
	return result
}

func env(key, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
}
