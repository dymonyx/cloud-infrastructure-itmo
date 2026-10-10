package observability

import (
	"net/http"
	"strconv"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

type Metrics struct {
	registry *prometheus.Registry
	requests *prometheus.CounterVec
	errors   *prometheus.CounterVec
	duration *prometheus.HistogramVec
}

func NewMetrics(service string) *Metrics {
	registry := prometheus.NewRegistry()

	requests := prometheus.NewCounterVec(prometheus.CounterOpts{
		Namespace:   "chainwise",
		Subsystem:   "http",
		Name:        "requests_total",
		Help:        "Total number of handled HTTP requests.",
		ConstLabels: prometheus.Labels{"service": service},
	}, []string{"method", "path", "status"})

	errors := prometheus.NewCounterVec(prometheus.CounterOpts{
		Namespace:   "chainwise",
		Subsystem:   "http",
		Name:        "errors_total",
		Help:        "Total number of HTTP responses with a 5xx status.",
		ConstLabels: prometheus.Labels{"service": service},
	}, []string{"method", "path", "status"})

	duration := prometheus.NewHistogramVec(prometheus.HistogramOpts{
		Namespace:   "chainwise",
		Subsystem:   "http",
		Name:        "request_duration_seconds",
		Help:        "HTTP request duration in seconds.",
		ConstLabels: prometheus.Labels{"service": service},
		Buckets:     []float64{0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 2, 3, 5},
	}, []string{"method", "path", "status"})

	registry.MustRegister(requests, errors, duration)

	// Create the demo series before the first request. Otherwise a short burst can
	// happen entirely between two Prometheus scrapes: the first observed value is
	// already the final value, so rate() has no earlier zero sample to compare it to.
	requests.WithLabelValues(http.MethodGet, "/bike/profile", "200").Add(0)
	requests.WithLabelValues(http.MethodGet, "/bike/demo/error", "500").Add(0)
	requests.WithLabelValues(http.MethodGet, "/bike/demo/delay", "200").Add(0)
	errors.WithLabelValues(http.MethodGet, "/bike/demo/error", "500").Add(0)
	duration.WithLabelValues(http.MethodGet, "/bike/profile", "200")
	duration.WithLabelValues(http.MethodGet, "/bike/demo/error", "500")
	duration.WithLabelValues(http.MethodGet, "/bike/demo/delay", "200")

	return &Metrics{registry: registry, requests: requests, errors: errors, duration: duration}
}

func (m *Metrics) Handler() http.Handler {
	return promhttp.HandlerFor(m.registry, promhttp.HandlerOpts{})
}

func (m *Metrics) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if isInternalPath(r.URL.Path) {
			next.ServeHTTP(w, r)
			return
		}

		started := time.Now()
		recorder := &metricRecorder{ResponseWriter: w, statusCode: http.StatusOK}
		next.ServeHTTP(recorder, r)

		status := strconv.Itoa(recorder.statusCode)
		m.requests.WithLabelValues(r.Method, r.URL.Path, status).Inc()
		m.duration.WithLabelValues(r.Method, r.URL.Path, status).Observe(time.Since(started).Seconds())
		if recorder.statusCode >= http.StatusInternalServerError {
			m.errors.WithLabelValues(r.Method, r.URL.Path, status).Inc()
		}
	})
}

func isInternalPath(path string) bool {
	return path == "/metrics" || path == "/healthz" || path == "/readyz" || path == "/internal/alerts"
}

type metricRecorder struct {
	http.ResponseWriter
	statusCode  int
	wroteHeader bool
}

func (r *metricRecorder) WriteHeader(statusCode int) {
	if r.wroteHeader {
		return
	}
	r.statusCode = statusCode
	r.wroteHeader = true
	r.ResponseWriter.WriteHeader(statusCode)
}
