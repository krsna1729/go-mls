// Package stream provides Prometheus metrics for monitoring.
package stream

import (
	"net/http"
	"sync"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

// Metrics holds all Prometheus metrics for the streaming system.
var Metrics = struct {
	// Relay metrics
	ActiveInputRelays  prometheus.Gauge
	ActiveOutputRelays prometheus.Gauge
	RelayStartTotal    *prometheus.CounterVec
	RelayStopTotal     *prometheus.CounterVec
	RelayErrorsTotal   *prometheus.CounterVec

	// Recording metrics
	ActiveRecordings    prometheus.Gauge
	RecordingStartTotal prometheus.Counter
	RecordingStopTotal  prometheus.Counter
	RecordingBytesTotal prometheus.Counter

	// HLS metrics
	ActiveHLSSessions prometheus.Gauge
	ActiveHLSViewers  prometheus.Gauge
	HLSSessionsTotal  prometheus.Counter

	// FFmpeg process metrics
	FFmpegProcessesActive   prometheus.Gauge
	FFmpegProcessDuration   *prometheus.HistogramVec
	FFmpegProcessErrorTotal *prometheus.CounterVec

	// HTTP request metrics
	HTTPRequestsTotal    *prometheus.CounterVec
	HTTPRequestDuration  *prometheus.HistogramVec
	HTTPRequestsInFlight prometheus.Gauge

	// System metrics (from existing Status API)
	SystemCPUPercent  prometheus.Gauge
	SystemMemoryBytes prometheus.Gauge
}{
	// Initialize relay metrics
	ActiveInputRelays: promauto.NewGauge(prometheus.GaugeOpts{
		Name: "gomls_active_input_relays",
		Help: "Number of currently active input relays",
	}),
	ActiveOutputRelays: promauto.NewGauge(prometheus.GaugeOpts{
		Name: "gomls_active_output_relays",
		Help: "Number of currently active output relays",
	}),
	RelayStartTotal: promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "gomls_relay_start_total",
		Help: "Total number of relay start operations",
	}, []string{"type"}), // type: input, output
	RelayStopTotal: promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "gomls_relay_stop_total",
		Help: "Total number of relay stop operations",
	}, []string{"type", "reason"}), // reason: user, refcount, error
	RelayErrorsTotal: promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "gomls_relay_errors_total",
		Help: "Total number of relay errors",
	}, []string{"type", "error_type"}),

	// Initialize recording metrics
	ActiveRecordings: promauto.NewGauge(prometheus.GaugeOpts{
		Name: "gomls_active_recordings",
		Help: "Number of currently active recordings",
	}),
	RecordingStartTotal: promauto.NewCounter(prometheus.CounterOpts{
		Name: "gomls_recording_start_total",
		Help: "Total number of recordings started",
	}),
	RecordingStopTotal: promauto.NewCounter(prometheus.CounterOpts{
		Name: "gomls_recording_stop_total",
		Help: "Total number of recordings stopped",
	}),
	RecordingBytesTotal: promauto.NewCounter(prometheus.CounterOpts{
		Name: "gomls_recording_bytes_total",
		Help: "Total bytes recorded",
	}),

	// Initialize HLS metrics
	ActiveHLSSessions: promauto.NewGauge(prometheus.GaugeOpts{
		Name: "gomls_active_hls_sessions",
		Help: "Number of currently active HLS sessions",
	}),
	ActiveHLSViewers: promauto.NewGauge(prometheus.GaugeOpts{
		Name: "gomls_active_hls_viewers",
		Help: "Number of currently active HLS viewers",
	}),
	HLSSessionsTotal: promauto.NewCounter(prometheus.CounterOpts{
		Name: "gomls_hls_sessions_total",
		Help: "Total number of HLS sessions created",
	}),

	// Initialize FFmpeg metrics
	FFmpegProcessesActive: promauto.NewGauge(prometheus.GaugeOpts{
		Name: "gomls_ffmpeg_processes_active",
		Help: "Number of currently running FFmpeg processes",
	}),
	FFmpegProcessDuration: promauto.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "gomls_ffmpeg_process_duration_seconds",
		Help:    "Duration of FFmpeg process execution",
		Buckets: []float64{1, 5, 10, 30, 60, 300, 600, 1800, 3600},
	}, []string{"type"}), // type: input, output, hls, recording
	FFmpegProcessErrorTotal: promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "gomls_ffmpeg_process_error_total",
		Help: "Total number of FFmpeg process errors",
	}, []string{"type", "exit_code"}),

	// Initialize HTTP metrics
	HTTPRequestsTotal: promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "gomls_http_requests_total",
		Help: "Total number of HTTP requests",
	}, []string{"method", "path", "status"}),
	HTTPRequestDuration: promauto.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "gomls_http_request_duration_seconds",
		Help:    "Duration of HTTP requests",
		Buckets: prometheus.DefBuckets,
	}, []string{"method", "path"}),
	HTTPRequestsInFlight: promauto.NewGauge(prometheus.GaugeOpts{
		Name: "gomls_http_requests_in_flight",
		Help: "Number of HTTP requests currently being processed",
	}),

	// Initialize system metrics
	SystemCPUPercent: promauto.NewGauge(prometheus.GaugeOpts{
		Name: "gomls_system_cpu_percent",
		Help: "Current system CPU usage percentage",
	}),
	SystemMemoryBytes: promauto.NewGauge(prometheus.GaugeOpts{
		Name: "gomls_system_memory_bytes",
		Help: "Current system memory usage in bytes",
	}),
}

// MetricsHandler returns the Prometheus metrics HTTP handler.
func MetricsHandler() http.Handler {
	return promhttp.Handler()
}

// MetricsMiddleware wraps an http.Handler to record request metrics.
func MetricsMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		Metrics.HTTPRequestsInFlight.Inc()
		defer Metrics.HTTPRequestsInFlight.Dec()

		start := time.Now()

		// Wrap ResponseWriter to capture status code
		wrapper := &responseWrapper{ResponseWriter: w, statusCode: http.StatusOK}

		next.ServeHTTP(wrapper, r)

		duration := time.Since(start).Seconds()

		// Normalize path to reduce cardinality
		path := normalizePath(r.URL.Path)

		Metrics.HTTPRequestsTotal.WithLabelValues(
			r.Method,
			path,
			http.StatusText(wrapper.statusCode),
		).Inc()

		Metrics.HTTPRequestDuration.WithLabelValues(
			r.Method,
			path,
		).Observe(duration)
	})
}

// responseWrapper captures the status code for metrics.
type responseWrapper struct {
	http.ResponseWriter
	statusCode    int
	wroteHeader   bool
	wroteHeaderMu sync.Mutex
}

func (rw *responseWrapper) WriteHeader(code int) {
	rw.wroteHeaderMu.Lock()
	defer rw.wroteHeaderMu.Unlock()
	if !rw.wroteHeader {
		rw.statusCode = code
		rw.wroteHeader = true
	}
	rw.ResponseWriter.WriteHeader(code)
}

func (rw *responseWrapper) Write(b []byte) (int, error) {
	rw.wroteHeaderMu.Lock()
	if !rw.wroteHeader {
		rw.statusCode = http.StatusOK
		rw.wroteHeader = true
	}
	rw.wroteHeaderMu.Unlock()
	return rw.ResponseWriter.Write(b)
}

// Flush implements the http.Flusher interface to allow SSE to work through the middleware
func (rw *responseWrapper) Flush() {
	if f, ok := rw.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

// normalizePath reduces URL path cardinality for metrics.
func normalizePath(path string) string {
	// Normalize common API patterns
	switch {
	case len(path) > 25 && path[:25] == "/api/relay/watch-input/hls/":
		return "/api/relay/watch-input/hls/{input}"
	case len(path) > 28 && path[:28] == "/api/v1/relay/watch-input/hls/":
		return "/api/v1/relay/watch-input/hls/{input}"
	default:
		return path
	}
}
