package middleware

import (
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/RhinoLink-Tech/data-fabric/internal/model"
	"github.com/gin-gonic/gin"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

// PrometheusMetrics holds all Prometheus metrics. Labels use bounded values:
// route templates, request methods, execution modes, result status, and the
// repository's finite database type list. Data source IDs and raw paths are
// intentionally not exported as labels.
type PrometheusMetrics struct {
	HttpRequestsTotal   *prometheus.CounterVec
	HttpRequestDuration *prometheus.HistogramVec
	HttpRequestSize     *prometheus.HistogramVec
	HttpResponseSize    *prometheus.HistogramVec

	QueryTotal    *prometheus.CounterVec
	QueryDuration *prometheus.HistogramVec
	QueryRowsRead *prometheus.CounterVec
	QueryErrors   *prometheus.CounterVec

	ConnectionPoolActive *prometheus.GaugeVec
	ConnectionPoolIdle   *prometheus.GaugeVec
	ConnectionPoolWait   *prometheus.HistogramVec

	DataSourceHealth *prometheus.GaugeVec
	DataSourceUp     *prometheus.GaugeVec

	ActiveStreams  *prometheus.GaugeVec
	StreamRowsSent *prometheus.CounterVec
	StreamDuration *prometheus.HistogramVec

	TokenRotations      *prometheus.CounterVec
	TokenRotationErrors *prometheus.CounterVec
}

var (
	metrics     *PrometheusMetrics
	metricsOnce sync.Once
)

// InitMetrics initializes and registers Prometheus metrics once per process.
func InitMetrics() {
	metricsOnce.Do(func() {
		metrics = &PrometheusMetrics{
			HttpRequestsTotal: promauto.NewCounterVec(
				prometheus.CounterOpts{
					Name: "data_fabric_http_requests_total",
					Help: "Total number of HTTP requests",
				},
				[]string{"method", "endpoint", "status"},
			),
			HttpRequestDuration: promauto.NewHistogramVec(
				prometheus.HistogramOpts{
					Name:    "data_fabric_http_request_duration_seconds",
					Help:    "HTTP request latency in seconds",
					Buckets: prometheus.DefBuckets,
				},
				[]string{"method", "endpoint"},
			),
			HttpRequestSize: promauto.NewHistogramVec(
				prometheus.HistogramOpts{
					Name:    "data_fabric_http_request_size_bytes",
					Help:    "HTTP request size in bytes",
					Buckets: []float64{100, 1000, 10000, 100000, 1000000},
				},
				[]string{"method", "endpoint"},
			),
			HttpResponseSize: promauto.NewHistogramVec(
				prometheus.HistogramOpts{
					Name:    "data_fabric_http_response_size_bytes",
					Help:    "HTTP response size in bytes",
					Buckets: []float64{100, 1000, 10000, 100000, 1000000},
				},
				[]string{"method", "endpoint"},
			),
			QueryTotal: promauto.NewCounterVec(
				prometheus.CounterOpts{
					Name: "data_fabric_query_total",
					Help: "Number of initial query executions",
				},
				[]string{"execution_mode", "status"},
			),
			QueryDuration: promauto.NewHistogramVec(
				prometheus.HistogramOpts{
					Name:    "data_fabric_query_duration_seconds",
					Help:    "Initial query execution time in seconds",
					Buckets: prometheus.DefBuckets,
				},
				[]string{"execution_mode"},
			),
			QueryRowsRead: promauto.NewCounterVec(
				prometheus.CounterOpts{
					Name: "data_fabric_query_rows_read_total",
					Help: "Rows returned in initial query batches",
				},
				[]string{"execution_mode"},
			),
			QueryErrors: promauto.NewCounterVec(
				prometheus.CounterOpts{
					Name: "data_fabric_query_errors_total",
					Help: "Number of failed initial query executions by bounded error category",
				},
				[]string{"execution_mode", "error_type"},
			),
			ConnectionPoolActive: promauto.NewGaugeVec(
				prometheus.GaugeOpts{
					Name: "data_fabric_connection_pool_active",
					Help: "Number of active connections in the pool",
				},
				[]string{"database_type"},
			),
			ConnectionPoolIdle: promauto.NewGaugeVec(
				prometheus.GaugeOpts{
					Name: "data_fabric_connection_pool_idle",
					Help: "Number of idle connections in the pool",
				},
				[]string{"database_type"},
			),
			ConnectionPoolWait: promauto.NewHistogramVec(
				prometheus.HistogramOpts{
					Name:    "data_fabric_connection_pool_wait_seconds",
					Help:    "Time spent waiting for a connection from the pool",
					Buckets: prometheus.DefBuckets,
				},
				[]string{"database_type"},
			),
			DataSourceHealth: promauto.NewGaugeVec(
				prometheus.GaugeOpts{
					Name: "data_fabric_datasource_health",
					Help: "Data source health status (1=healthy, 0=unhealthy)",
				},
				[]string{"database_type"},
			),
			DataSourceUp: promauto.NewGaugeVec(
				prometheus.GaugeOpts{
					Name: "data_fabric_datasource_up",
					Help: "Whether a data source type is up (1=up, 0=down)",
				},
				[]string{"database_type"},
			),
			ActiveStreams: promauto.NewGaugeVec(
				prometheus.GaugeOpts{
					Name: "data_fabric_streaming_active",
					Help: "Number of active streaming queries",
				},
				[]string{"database_type"},
			),
			StreamRowsSent: promauto.NewCounterVec(
				prometheus.CounterOpts{
					Name: "data_fabric_streaming_rows_sent_total",
					Help: "Number of rows sent via streaming",
				},
				[]string{"database_type"},
			),
			StreamDuration: promauto.NewHistogramVec(
				prometheus.HistogramOpts{
					Name:    "data_fabric_streaming_duration_seconds",
					Help:    "Streaming query duration in seconds",
					Buckets: prometheus.DefBuckets,
				},
				[]string{"database_type"},
			),
			TokenRotations: promauto.NewCounterVec(
				prometheus.CounterOpts{
					Name: "data_fabric_token_rotations_total",
					Help: "Total number of token rotations",
				},
				[]string{"database_type"},
			),
			TokenRotationErrors: promauto.NewCounterVec(
				prometheus.CounterOpts{
					Name: "data_fabric_token_rotation_errors_total",
					Help: "Number of token rotation failures",
				},
				[]string{"database_type"},
			),
		}
	})
}

// GetMetrics returns the initialized metrics.
func GetMetrics() *PrometheusMetrics {
	return metrics
}

// PrometheusHandler returns the default Prometheus registry scrape handler.
func PrometheusHandler() http.Handler {
	return promhttp.Handler()
}

// PrometheusMiddleware records one HTTP observation per completed request.
func PrometheusMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		if metrics == nil {
			c.Next()
			return
		}

		started := time.Now()
		c.Next()

		method := normalizeHTTPMethod(c.Request.Method)
		endpoint := c.FullPath()
		if endpoint == "" {
			endpoint = "unmatched"
		}
		status := strconv.Itoa(c.Writer.Status())
		duration := time.Since(started).Seconds()

		metrics.HttpRequestsTotal.WithLabelValues(method, endpoint, status).Inc()
		metrics.HttpRequestDuration.WithLabelValues(method, endpoint).Observe(duration)
		if c.Request.ContentLength > 0 {
			metrics.HttpRequestSize.WithLabelValues(method, endpoint).Observe(float64(c.Request.ContentLength))
		}
		if c.Writer.Size() > 0 {
			metrics.HttpResponseSize.WithLabelValues(method, endpoint).Observe(float64(c.Writer.Size()))
		}
	}
}

// RecordQueryMetrics records one initial query execution. Continuation batches
// are covered by HTTP request metrics but do not increment the query counter.
func RecordQueryMetrics(executionMode, status string, duration time.Duration, rowsRead int64) {
	if metrics == nil {
		return
	}

	executionMode = normalizeExecutionMode(executionMode)
	status = normalizeQueryStatus(status)
	metrics.QueryTotal.WithLabelValues(executionMode, status).Inc()
	metrics.QueryDuration.WithLabelValues(executionMode).Observe(duration.Seconds())
	if status == "success" && rowsRead > 0 {
		metrics.QueryRowsRead.WithLabelValues(executionMode).Add(float64(rowsRead))
	}
}

// RecordQueryError records a failed query using a bounded category label.
func RecordQueryError(executionMode, errorType string) {
	if metrics == nil {
		return
	}
	metrics.QueryErrors.WithLabelValues(normalizeExecutionMode(executionMode), normalizeQueryErrorType(errorType)).Inc()
}

// UpdateConnectionPoolMetrics updates aggregate connection pool gauges by driver type.
func UpdateConnectionPoolMetrics(_ string, databaseType string, active, idle int) {
	if metrics == nil {
		return
	}
	databaseType = normalizeDatabaseType(databaseType)
	metrics.ConnectionPoolActive.WithLabelValues(databaseType).Set(float64(active))
	metrics.ConnectionPoolIdle.WithLabelValues(databaseType).Set(float64(idle))
}

// UpdateDataSourceHealth updates aggregate data source health gauges by driver type.
func UpdateDataSourceHealth(_ string, databaseType string, healthy, up bool) {
	if metrics == nil {
		return
	}
	databaseType = normalizeDatabaseType(databaseType)
	healthValue := 0.0
	if healthy {
		healthValue = 1.0
	}
	metrics.DataSourceHealth.WithLabelValues(databaseType).Set(healthValue)

	upValue := 0.0
	if up {
		upValue = 1.0
	}
	metrics.DataSourceUp.WithLabelValues(databaseType).Set(upValue)
}

// UpdateStreamingMetrics updates aggregate streaming metrics by driver type.
func UpdateStreamingMetrics(_ string, databaseType string, activeStreams int, rowsSent int64, duration time.Duration) {
	if metrics == nil {
		return
	}
	databaseType = normalizeDatabaseType(databaseType)
	metrics.ActiveStreams.WithLabelValues(databaseType).Set(float64(activeStreams))
	metrics.StreamRowsSent.WithLabelValues(databaseType).Add(float64(rowsSent))
	metrics.StreamDuration.WithLabelValues(databaseType).Observe(duration.Seconds())
}

// RecordTokenRotation records a token rotation event by driver type.
func RecordTokenRotation(_ string, databaseType string, success bool) {
	if metrics == nil {
		return
	}
	databaseType = normalizeDatabaseType(databaseType)
	if success {
		metrics.TokenRotations.WithLabelValues(databaseType).Inc()
	} else {
		metrics.TokenRotationErrors.WithLabelValues(databaseType).Inc()
	}
}

func normalizeHTTPMethod(method string) string {
	switch method {
	case http.MethodGet, http.MethodPost, http.MethodPut, http.MethodDelete, http.MethodPatch,
		http.MethodHead, http.MethodOptions, http.MethodConnect, http.MethodTrace:
		return method
	default:
		return "OTHER"
	}
}

func normalizeExecutionMode(mode string) string {
	switch strings.ToLower(strings.TrimSpace(mode)) {
	case "auto":
		return "auto"
	case "pagination":
		return "pagination"
	case "streaming":
		return "streaming"
	default:
		return "other"
	}
}

func normalizeQueryStatus(status string) string {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case "success":
		return "success"
	case "error":
		return "error"
	default:
		return "other"
	}
}

func normalizeQueryErrorType(errorType string) string {
	switch strings.ToLower(strings.TrimSpace(errorType)) {
	case "execution_error":
		return "execution_error"
	case "timeout":
		return "timeout"
	case "canceled":
		return "canceled"
	case "validation":
		return "validation"
	case "access_denied":
		return "access_denied"
	default:
		return "other"
	}
}

func normalizeDatabaseType(databaseType string) string {
	databaseType = strings.TrimSpace(databaseType)
	if model.IsValidDatabaseType(databaseType) {
		return databaseType
	}
	return "other"
}
