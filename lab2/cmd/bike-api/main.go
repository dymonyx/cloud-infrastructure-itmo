package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"sync"
	"syscall"
	"time"

	"chainwise-lab2/internal/httpx"
	"chainwise-lab2/internal/model"
	"chainwise-lab2/internal/observability"
	"chainwise-lab2/internal/web"

	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
)

const serviceName = "chainwise-bike-api"

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil)).With("service", serviceName)
	port := env("PORT", "8081")
	otlpEndpoint := env("OTEL_EXPORTER_OTLP_ENDPOINT", "http://localhost:4318")
	selfURL := env("SELF_URL", "http://localhost:"+port)

	ctx := context.Background()
	shutdownTracing, err := observability.InitTracing(ctx, serviceName, otlpEndpoint)
	if err != nil {
		logger.Error("tracing initialization failed", "error", err)
		os.Exit(1)
	}

	metrics := observability.NewMetrics(serviceName)
	mux := http.NewServeMux()
	mux.Handle("/", web.Handler())
	mux.Handle("/metrics", metrics.Handler())
	mux.HandleFunc("/healthz", healthHandler)
	mux.HandleFunc("/readyz", healthHandler)
	mux.HandleFunc("/bike/profile", profileHandler)
	mux.HandleFunc("/bike/check", checkHandler)
	mux.HandleFunc("/bike/demo/error", errorHandler(logger))
	mux.HandleFunc("/bike/demo/delay", delayHandler(logger))
	mux.HandleFunc("/bike/demo/load", loadHandler(selfURL, logger))
	mux.HandleFunc("/internal/alerts", alertWebhookHandler(logger))

	applicationHandler := httpx.Chain(
		mux,
		httpx.Recover(logger),
		httpx.AccessLog(logger),
		metrics.Middleware,
	)
	handler := otelhttp.NewHandler(applicationHandler, "http.server",
		otelhttp.WithSpanNameFormatter(func(_ string, r *http.Request) string {
			return r.Method + " " + r.URL.Path
		}),
	)

	server := &http.Server{
		Addr:              ":" + port,
		Handler:           handler,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       15 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       60 * time.Second,
	}

	errCh := make(chan error, 1)
	go func() {
		logger.Info("service started", "port", port, "otlp_endpoint", otlpEndpoint)
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
		logger.Error("http server failed", "error", err)
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	_ = server.Shutdown(shutdownCtx)
	_ = shutdownTracing(shutdownCtx)
}

func healthHandler(w http.ResponseWriter, _ *http.Request) {
	httpx.WriteJSON(w, http.StatusOK, model.HealthResponse{Service: serviceName, Status: "ok"})
}

func profileHandler(w http.ResponseWriter, _ *http.Request) {
	httpx.WriteJSON(w, http.StatusOK, demoProfile())
}

func checkHandler(w http.ResponseWriter, r *http.Request) {
	ctx, span := otel.Tracer(serviceName).Start(r.Context(), "maintenance-recommendation")
	defer span.End()

	select {
	case <-time.After(120 * time.Millisecond):
	case <-ctx.Done():
		return
	}

	httpx.WriteJSON(w, http.StatusOK, model.BikeCheckResponse{
		BikeProfile:    demoProfile(),
		Recommendation: "Проверить давление в шинах перед следующей поездкой",
		Priority:       "normal",
	})
}

func errorHandler(logger *slog.Logger) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		err := errors.New("demo maintenance calculation failure")
		span := trace.SpanFromContext(r.Context())
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		logger.Error("demo error triggered", "trace_id", httpx.TraceID(r), "error", err)
		httpx.WriteError(w, http.StatusInternalServerError, err.Error(), httpx.TraceID(r))
	}
}

func delayHandler(logger *slog.Logger) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx, span := otel.Tracer(serviceName).Start(r.Context(), "slow-dependency")
		defer span.End()

		delay := 2 * time.Second
		logger.Warn("demo delay triggered", "trace_id", httpx.TraceID(r), "delay_ms", delay.Milliseconds())
		select {
		case <-time.After(delay):
		case <-ctx.Done():
			return
		}

		httpx.WriteJSON(w, http.StatusOK, model.MessageResponse{
			Status:  "ok",
			Message: "Искусственная задержка завершена",
			TraceID: httpx.TraceID(r),
		})
	}
}

func loadHandler(selfURL string, logger *slog.Logger) http.HandlerFunc {
	client := &http.Client{
		Timeout:   5 * time.Second,
		Transport: otelhttp.NewTransport(http.DefaultTransport),
	}

	return func(w http.ResponseWriter, r *http.Request) {
		count := intQuery(r, "count", 120, 1, 500)
		concurrency := 20
		semaphore := make(chan struct{}, concurrency)
		var wg sync.WaitGroup
		var failures int
		var failuresMu sync.Mutex

		for i := 0; i < count; i++ {
			wg.Add(1)
			semaphore <- struct{}{}
			go func(requestNumber int) {
				defer wg.Done()
				defer func() { <-semaphore }()

				url := fmt.Sprintf("%s/bike/profile?request=%d", selfURL, requestNumber)
				request, err := http.NewRequestWithContext(r.Context(), http.MethodGet, url, nil)
				if err == nil {
					var response *http.Response
					response, err = client.Do(request)
					if response != nil {
						_ = response.Body.Close()
					}
				}
				if err != nil {
					failuresMu.Lock()
					failures++
					failuresMu.Unlock()
				}
			}(i)
		}
		wg.Wait()

		logger.Info("demo load completed", "trace_id", httpx.TraceID(r), "requests", count, "failures", failures)
		httpx.WriteJSON(w, http.StatusOK, model.MessageResponse{
			Status:  "ok",
			Message: fmt.Sprintf("Создано запросов: %d, ошибок клиента: %d", count, failures),
			TraceID: httpx.TraceID(r),
		})
	}
}

func alertWebhookHandler(logger *slog.Logger) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			httpx.WriteError(w, http.StatusMethodNotAllowed, "POST is required", httpx.TraceID(r))
			return
		}

		var payload map[string]any
		decoder := json.NewDecoder(io.LimitReader(r.Body, 1<<20))
		if err := decoder.Decode(&payload); err != nil {
			httpx.WriteError(w, http.StatusBadRequest, "invalid Alertmanager payload", httpx.TraceID(r))
			return
		}

		logger.Warn("alertmanager notification received",
			"trace_id", httpx.TraceID(r),
			"payload", payload,
		)
		httpx.WriteJSON(w, http.StatusOK, model.MessageResponse{
			Status:  "accepted",
			Message: "Alertmanager notification received",
			TraceID: httpx.TraceID(r),
		})
	}
}

func demoProfile() model.BikeProfile {
	return model.BikeProfile{
		ID:                "bike-demo-001",
		Name:              "Chainwise Gravel",
		Type:              "gravel",
		CurrentOdometerKM: 1335,
		RidingStyle:       "daily commuting",
	}
}

func intQuery(r *http.Request, key string, fallback, minimum, maximum int) int {
	value, err := strconv.Atoi(r.URL.Query().Get(key))
	if err != nil {
		return fallback
	}
	if value < minimum {
		return minimum
	}
	if value > maximum {
		return maximum
	}
	return value
}

func env(key, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
}
