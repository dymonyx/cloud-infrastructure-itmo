package httpx

import (
	"log/slog"
	"net/http"
	"runtime/debug"
	"time"

	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
)

type Middleware func(http.Handler) http.Handler

func Chain(handler http.Handler, middlewares ...Middleware) http.Handler {
	for i := len(middlewares) - 1; i >= 0; i-- {
		handler = middlewares[i](handler)
	}
	return handler
}

func Recover(logger *slog.Logger) Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			defer func() {
				if recovered := recover(); recovered != nil {
					span := trace.SpanFromContext(r.Context())
					span.SetStatus(codes.Error, "panic recovered")
					logger.Error("panic recovered",
						"trace_id", TraceID(r),
						"panic", recovered,
						"stack", string(debug.Stack()),
					)
					WriteError(w, http.StatusInternalServerError, "internal server error", TraceID(r))
				}
			}()
			next.ServeHTTP(w, r)
		})
	}
}

func AccessLog(logger *slog.Logger) Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			started := time.Now()
			recorder := &statusRecorder{ResponseWriter: w, statusCode: http.StatusOK}
			if traceID := TraceID(r); traceID != "" {
				recorder.Header().Set("X-Trace-ID", traceID)
			}

			next.ServeHTTP(recorder, r)

			logger.Info("http request",
				"trace_id", TraceID(r),
				"method", r.Method,
				"path", r.URL.Path,
				"status", recorder.statusCode,
				"duration_ms", time.Since(started).Milliseconds(),
			)
		})
	}
}

func TraceID(r *http.Request) string {
	spanContext := trace.SpanContextFromContext(r.Context())
	if !spanContext.IsValid() {
		return ""
	}
	return spanContext.TraceID().String()
}

type statusRecorder struct {
	http.ResponseWriter
	statusCode  int
	wroteHeader bool
}

func (r *statusRecorder) WriteHeader(statusCode int) {
	if r.wroteHeader {
		return
	}
	r.statusCode = statusCode
	r.wroteHeader = true
	r.ResponseWriter.WriteHeader(statusCode)
}
