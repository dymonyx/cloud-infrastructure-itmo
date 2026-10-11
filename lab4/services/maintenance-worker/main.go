package main

import (
	"context"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
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

type processedTrip struct {
	tripRecorded
	Partition   int32     `json:"partition"`
	Offset      int64     `json:"offset"`
	Instance    string    `json:"instance"`
	ProcessedAt time.Time `json:"processed_at"`
	Result      string    `json:"result"`
}

type store struct {
	mu      sync.RWMutex
	items   []processedTrip
	ready   bool
	claims  map[string][]int32
	groupID string
	topic   string
}

type consumerHandler struct {
	logger    *slog.Logger
	store     *store
	instance  string
	delay     time.Duration
	processed *prometheus.CounterVec
	duration  prometheus.Histogram
}

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil)).With("service", "maintenance-worker")
	brokers := splitCSV(env("KAFKA_BROKERS", "localhost:9092"))
	topic := env("KAFKA_TOPIC", "trip-recorded")
	group := env("KAFKA_GROUP", "maintenance-workers")
	instance := env("INSTANCE_NAME", hostname())
	port := env("PORT", "8082")
	delay := time.Duration(envInt("PROCESSING_DELAY_MS", 800)) * time.Millisecond

	registry := prometheus.NewRegistry()
	processed := prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "chainwise_trips_processed_total",
		Help: "Number of trip-recorded events processed by a maintenance worker.",
	}, []string{"instance", "partition"})
	duration := prometheus.NewHistogram(prometheus.HistogramOpts{
		Name:    "chainwise_trip_processing_duration_seconds",
		Help:    "Time spent processing a trip-recorded event.",
		Buckets: prometheus.DefBuckets,
	})
	registry.MustRegister(processed, duration, prometheus.NewGoCollector(), prometheus.NewProcessCollector(prometheus.ProcessCollectorOpts{}))

	state := &store{claims: make(map[string][]int32), groupID: group, topic: topic}
	handler := &consumerHandler{logger: logger, store: state, instance: instance, delay: delay, processed: processed, duration: duration}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	groupClient, err := newConsumerGroupWithRetry(brokers, group, instance, 60*time.Second)
	if err != nil {
		logger.Error("cannot connect to Kafka", "error", err)
		os.Exit(1)
	}
	defer groupClient.Close()

	go consumeLoop(ctx, logger, groupClient, topic, handler)

	mux := http.NewServeMux()
	mux.HandleFunc("GET /", serveIndex)
	mux.HandleFunc("GET /api/processed", func(w http.ResponseWriter, _ *http.Request) {
		state.mu.RLock()
		items := append([]processedTrip(nil), state.items...)
		claims := copyClaims(state.claims)
		ready := state.ready
		state.mu.RUnlock()
		writeJSON(w, http.StatusOK, map[string]any{
			"instance": instance, "group": group, "topic": topic, "ready": ready, "claims": claims, "items": items,
		})
	})
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, map[string]string{"service": "maintenance-worker", "instance": instance, "status": "ok"})
	})
	mux.Handle("GET /metrics", promhttp.HandlerFor(registry, promhttp.HandlerOpts{}))

	server := &http.Server{
		Addr:              ":" + port,
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       10 * time.Second,
		WriteTimeout:      10 * time.Second,
		IdleTimeout:       60 * time.Second,
	}
	errCh := make(chan error, 1)
	go func() {
		logger.Info("maintenance-worker started", "instance", instance, "port", port, "topic", topic, "group", group)
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
	cancel()
	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer shutdownCancel()
	_ = server.Shutdown(shutdownCtx)
}

func (handler *consumerHandler) Setup(session sarama.ConsumerGroupSession) error {
	handler.store.mu.Lock()
	handler.store.ready = true
	handler.store.claims = copyClaims(session.Claims())
	handler.store.mu.Unlock()
	handler.logger.Info("consumer group assignment received", "instance", handler.instance, "claims", session.Claims())
	return nil
}

func (handler *consumerHandler) Cleanup(_ sarama.ConsumerGroupSession) error {
	handler.store.mu.Lock()
	handler.store.ready = false
	handler.store.claims = make(map[string][]int32)
	handler.store.mu.Unlock()
	return nil
}

func (handler *consumerHandler) ConsumeClaim(session sarama.ConsumerGroupSession, claim sarama.ConsumerGroupClaim) error {
	for message := range claim.Messages() {
		started := time.Now()
		var event tripRecorded
		if err := json.Unmarshal(message.Value, &event); err != nil {
			handler.logger.Error("invalid event", "partition", message.Partition, "offset", message.Offset, "error", err)
			session.MarkMessage(message, "invalid-json")
			continue
		}

		select {
		case <-time.After(handler.delay):
		case <-session.Context().Done():
			return nil
		}

		result := processedTrip{
			tripRecorded: event,
			Partition:    message.Partition,
			Offset:       message.Offset,
			Instance:     handler.instance,
			ProcessedAt:  time.Now().UTC(),
			Result:       maintenanceResult(event.DistanceKM),
		}
		handler.store.mu.Lock()
		handler.store.items = append([]processedTrip{result}, handler.store.items...)
		if len(handler.store.items) > 100 {
			handler.store.items = handler.store.items[:100]
		}
		handler.store.mu.Unlock()
		handler.processed.WithLabelValues(handler.instance, strconv.Itoa(int(message.Partition))).Inc()
		handler.duration.Observe(time.Since(started).Seconds())
		handler.logger.Info("trip processed", "trip_id", event.TripID, "instance", handler.instance, "partition", message.Partition, "offset", message.Offset)
		session.MarkMessage(message, "processed")
	}
	return nil
}

func consumeLoop(ctx context.Context, logger *slog.Logger, group sarama.ConsumerGroup, topic string, handler sarama.ConsumerGroupHandler) {
	for ctx.Err() == nil {
		if err := group.Consume(ctx, []string{topic}, handler); err != nil && ctx.Err() == nil {
			logger.Error("consumer group cycle failed", "error", err)
			time.Sleep(time.Second)
		}
	}
}

func newConsumerGroupWithRetry(brokers []string, group, instance string, timeout time.Duration) (sarama.ConsumerGroup, error) {
	deadline := time.Now().Add(timeout)
	var lastErr error
	for time.Now().Before(deadline) {
		config := sarama.NewConfig()
		config.ClientID = "chainwise-" + instance
		config.Consumer.Group.InstanceId = instance
		config.Consumer.Group.Rebalance.Strategy = sarama.NewBalanceStrategyRoundRobin()
		config.Consumer.Offsets.Initial = sarama.OffsetOldest
		config.Consumer.Offsets.AutoCommit.Enable = true
		config.Consumer.Offsets.AutoCommit.Interval = time.Second
		client, err := sarama.NewConsumerGroup(brokers, group, config)
		if err == nil {
			return client, nil
		}
		lastErr = err
		time.Sleep(2 * time.Second)
	}
	return nil, fmt.Errorf("Kafka unavailable after %s: %w", timeout, lastErr)
}

func maintenanceResult(distance float64) string {
	switch {
	case distance >= 50:
		return "Проверить цепь и давление в шинах"
	case distance >= 20:
		return "Проверить состояние цепи"
	default:
		return "Дополнительное обслуживание не требуется"
	}
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

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func copyClaims(source map[string][]int32) map[string][]int32 {
	result := make(map[string][]int32, len(source))
	for topic, partitions := range source {
		result[topic] = append([]int32(nil), partitions...)
	}
	return result
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

func hostname() string {
	name, err := os.Hostname()
	if err != nil || name == "" {
		return "maintenance-worker"
	}
	return name
}

func envInt(key string, fallback int) int {
	value, err := strconv.Atoi(os.Getenv(key))
	if err != nil {
		return fallback
	}
	return value
}

func env(key, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
}
