package main

import (
	"bufio"
	"context"
	"crypto/subtle"
	"fmt"
	"net"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
)

const requestIDContextKey contextKey = "request-id"

type contextKey string

type httpMetricKey struct {
	Method string
	Route  string
	Status int
}

type httpMetricValue struct {
	Count           uint64
	DurationSeconds float64
}

type metricsRegistry struct {
	mu          sync.Mutex
	startedAt   time.Time
	inFlight    int
	requests    map[httpMetricKey]httpMetricValue
	outboxEvent map[string]uint64
	rateLimited uint64
}

func newMetricsRegistry() *metricsRegistry {
	return &metricsRegistry{
		startedAt:   time.Now(),
		requests:    make(map[httpMetricKey]httpMetricValue),
		outboxEvent: make(map[string]uint64),
	}
}

func (registry *metricsRegistry) requestStarted() {
	registry.mu.Lock()
	registry.inFlight++
	registry.mu.Unlock()
}

func (registry *metricsRegistry) observeRequest(method, route string, status int, duration time.Duration) {
	registry.mu.Lock()
	defer registry.mu.Unlock()
	registry.inFlight--
	key := httpMetricKey{Method: method, Route: route, Status: status}
	value := registry.requests[key]
	value.Count++
	value.DurationSeconds += duration.Seconds()
	registry.requests[key] = value
}

func (registry *metricsRegistry) recordOutboxEvent(eventType string) {
	registry.mu.Lock()
	registry.outboxEvent[eventType]++
	registry.mu.Unlock()
}

func (registry *metricsRegistry) recordRateLimitRejection() {
	registry.mu.Lock()
	registry.rateLimited++
	registry.mu.Unlock()
}

func (app *application) requestIDMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestID := incomingRequestID(r.Header.Get("X-Request-ID"))
		w.Header().Set("X-Request-ID", requestID)
		r = r.WithContext(context.WithValue(r.Context(), requestIDContextKey, requestID))

		route := metricRoute(r.URL.Path)
		startedAt := time.Now()
		app.metrics.requestStarted()
		response := &observabilityResponseWriter{ResponseWriter: w}
		defer func() {
			status := response.status
			if status == 0 {
				status = http.StatusOK
			}
			duration := time.Since(startedAt)
			app.metrics.observeRequest(r.Method, route, status, duration)
			app.logger.Info("http_request",
				"request_id", requestID,
				"method", r.Method,
				"route", route,
				"status", status,
				"duration_ms", duration.Milliseconds(),
			)
		}()
		next.ServeHTTP(response, r)
	})
}

func requestIDFromContext(ctx context.Context) string {
	requestID, _ := ctx.Value(requestIDContextKey).(string)
	return requestID
}

func incomingRequestID(requestID string) string {
	if len(requestID) < 8 || len(requestID) > 128 {
		return bsonRequestID()
	}
	for _, character := range requestID {
		if (character < 'a' || character > 'z') &&
			(character < 'A' || character > 'Z') &&
			(character < '0' || character > '9') &&
			character != '-' && character != '_' {
			return bsonRequestID()
		}
	}
	return requestID
}

func bsonRequestID() string {
	return bson.NewObjectID().Hex()
}

func metricRoute(path string) string {
	switch {
	case path == "/healthz":
		return "/healthz"
	case path == "/readyz":
		return "/readyz"
	case path == "/metrics":
		return "/metrics"
	case path == "/.well-known/m-stash.json":
		return "/.well-known/m-stash.json"
	case path == "/v1/auth/signup":
		return "/v1/auth/signup"
	case path == "/v1/auth/login":
		return "/v1/auth/login"
	case path == "/v1/auth/verify":
		return "/v1/auth/verify"
	case path == "/v1/me/namespace":
		return "/v1/me/namespace"
	case strings.HasPrefix(path, "/v1/me/resources/"):
		return "/v1/me/resources/{type}/{slug}"
	case strings.HasPrefix(path, "/v1/public/"):
		return "/v1/public/{username}/{type}/{slug}"
	default:
		return "unmatched"
	}
}

func (app *application) handleMetrics(w http.ResponseWriter, r *http.Request) {
	if !requireMethod(w, r, http.MethodGet) {
		return
	}
	if app.config.MetricsToken == "" || !app.validMetricsToken(r.Header.Get("Authorization")) {
		http.NotFound(w, r)
		return
	}

	w.Header().Set("Content-Type", "text/plain; version=0.0.4; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(app.metrics.prometheus())
}

func (app *application) validMetricsToken(authorization string) bool {
	providedToken, ok := strings.CutPrefix(authorization, "Bearer ")
	if !ok {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(providedToken), []byte(app.config.MetricsToken)) == 1
}

func (registry *metricsRegistry) prometheus() []byte {
	registry.mu.Lock()
	defer registry.mu.Unlock()

	var builder strings.Builder
	builder.WriteString("# HELP m_stash_http_requests_total HTTP requests completed by normalized route and status.\n")
	builder.WriteString("# TYPE m_stash_http_requests_total counter\n")
	keys := make([]httpMetricKey, 0, len(registry.requests))
	for key := range registry.requests {
		keys = append(keys, key)
	}
	sort.Slice(keys, func(left, right int) bool {
		return fmt.Sprintf("%s|%s|%03d", keys[left].Method, keys[left].Route, keys[left].Status) < fmt.Sprintf("%s|%s|%03d", keys[right].Method, keys[right].Route, keys[right].Status)
	})
	for _, key := range keys {
		value := registry.requests[key]
		labels := fmt.Sprintf(`method=%q,route=%q,status=%q`, key.Method, key.Route, fmt.Sprint(key.Status))
		fmt.Fprintf(&builder, "m_stash_http_requests_total{%s} %d\n", labels, value.Count)
	}
	builder.WriteString("# HELP m_stash_http_request_duration_seconds Total HTTP request duration by normalized route and status.\n")
	builder.WriteString("# TYPE m_stash_http_request_duration_seconds summary\n")
	for _, key := range keys {
		value := registry.requests[key]
		labels := fmt.Sprintf(`method=%q,route=%q,status=%q`, key.Method, key.Route, fmt.Sprint(key.Status))
		fmt.Fprintf(&builder, "m_stash_http_request_duration_seconds_sum{%s} %.9f\n", labels, value.DurationSeconds)
		fmt.Fprintf(&builder, "m_stash_http_request_duration_seconds_count{%s} %d\n", labels, value.Count)
	}
	fmt.Fprintf(&builder, "# TYPE m_stash_http_in_flight_requests gauge\nm_stash_http_in_flight_requests %d\n", registry.inFlight)
	builder.WriteString("# HELP m_stash_outbox_events_total Outbox events committed by event type.\n")
	builder.WriteString("# TYPE m_stash_outbox_events_total counter\n")
	eventTypes := make([]string, 0, len(registry.outboxEvent))
	for eventType := range registry.outboxEvent {
		eventTypes = append(eventTypes, eventType)
	}
	sort.Strings(eventTypes)
	for _, eventType := range eventTypes {
		fmt.Fprintf(&builder, "m_stash_outbox_events_total{type=%q} %d\n", eventType, registry.outboxEvent[eventType])
	}
	builder.WriteString("# HELP m_stash_auth_rate_limit_rejections_total Authentication attempts rejected by the shared limiter.\n")
	builder.WriteString("# TYPE m_stash_auth_rate_limit_rejections_total counter\n")
	fmt.Fprintf(&builder, "m_stash_auth_rate_limit_rejections_total %d\n", registry.rateLimited)
	fmt.Fprintf(&builder, "# TYPE m_stash_process_start_time_seconds gauge\nm_stash_process_start_time_seconds %d\n", registry.startedAt.Unix())
	return []byte(builder.String())
}

type observabilityResponseWriter struct {
	http.ResponseWriter
	status int
}

func (writer *observabilityResponseWriter) WriteHeader(status int) {
	if writer.status == 0 {
		writer.status = status
	}
	writer.ResponseWriter.WriteHeader(status)
}

func (writer *observabilityResponseWriter) Write(body []byte) (int, error) {
	if writer.status == 0 {
		writer.WriteHeader(http.StatusOK)
	}
	return writer.ResponseWriter.Write(body)
}

func (writer *observabilityResponseWriter) Unwrap() http.ResponseWriter {
	return writer.ResponseWriter
}

func (writer *observabilityResponseWriter) Flush() {
	if flusher, ok := writer.ResponseWriter.(http.Flusher); ok {
		flusher.Flush()
	}
}

func (writer *observabilityResponseWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	hijacker, ok := writer.ResponseWriter.(http.Hijacker)
	if !ok {
		return nil, nil, http.ErrNotSupported
	}
	return hijacker.Hijack()
}
