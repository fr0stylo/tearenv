package server

import (
	"fmt"
	"time"

	"github.com/fr0stylo/tearenv/internal/authorization"
	"github.com/prometheus/client_golang/prometheus"
)

const (
	metricDirectionDown = "down"
	metricDirectionUp   = "up"

	metricResultDenied            = "denied"
	metricResultDialError         = "dial_error"
	metricResultError             = "error"
	metricResultRejected          = "rejected"
	metricResultScaleError        = "scale_error"
	metricResultScalerUnavailable = "scaler_unavailable"
	metricResultSuccess           = "success"

	metricServiceManaged = "managed"
	metricServiceStatic  = "static"
	metricServiceUnknown = "unknown"
)

// Metrics contains the bounded-cardinality Prometheus instrumentation shared
// by the SSH server and service lifecycle engine.
type Metrics struct {
	ready                  prometheus.Gauge
	authenticationAttempts *prometheus.CounterVec
	handshakeFailures      prometheus.Counter
	catalogRequests        *prometheus.CounterVec
	environmentProvisions  *prometheus.CounterVec
	environmentDuration    *prometheus.HistogramVec
	serviceOpens           *prometheus.CounterVec
	serviceOpenDuration    *prometheus.HistogramVec
	activeConnections      *prometheus.GaugeVec
	scaleOperations        *prometheus.CounterVec
	scaleOperationDuration *prometheus.HistogramVec
	managedWorkloads       prometheus.Gauge
}

// NewMetrics registers the daemon and engine collectors with registerer.
func NewMetrics(registerer prometheus.Registerer) (*Metrics, error) {
	metrics := &Metrics{
		ready: prometheus.NewGauge(prometheus.GaugeOpts{
			Namespace: "tearenv",
			Subsystem: "daemon",
			Name:      "ready",
			Help:      "Whether the daemon has started its configured listeners.",
		}),
		authenticationAttempts: prometheus.NewCounterVec(prometheus.CounterOpts{
			Namespace: "tearenv",
			Subsystem: "ssh",
			Name:      "authentication_attempts_total",
			Help:      "Total SSH authentication attempts by method and result.",
		}, []string{"method", "result"}),
		handshakeFailures: prometheus.NewCounter(prometheus.CounterOpts{
			Namespace: "tearenv",
			Subsystem: "ssh",
			Name:      "handshake_failures_total",
			Help:      "Total SSH connections that failed during the handshake.",
		}),
		catalogRequests: prometheus.NewCounterVec(prometheus.CounterOpts{
			Namespace: "tearenv",
			Subsystem: "engine",
			Name:      "service_catalog_requests_total",
			Help:      "Total service catalog requests by result.",
		}, []string{"result"}),
		environmentProvisions: prometheus.NewCounterVec(prometheus.CounterOpts{
			Namespace: "tearenv",
			Subsystem: "engine",
			Name:      "environment_provision_attempts_total",
			Help:      "Total authenticated environment reconciliation attempts by result.",
		}, []string{"result"}),
		environmentDuration: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Namespace: "tearenv",
			Subsystem: "engine",
			Name:      "environment_provision_duration_seconds",
			Help:      "Time spent reconciling an authenticated identity environment.",
			Buckets:   prometheus.DefBuckets,
		}, []string{"result"}),
		serviceOpens: prometheus.NewCounterVec(prometheus.CounterOpts{
			Namespace: "tearenv",
			Subsystem: "engine",
			Name:      "service_open_attempts_total",
			Help:      "Total service connection attempts by service type and result.",
		}, []string{"service_type", "result"}),
		serviceOpenDuration: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Namespace: "tearenv",
			Subsystem: "engine",
			Name:      "service_open_duration_seconds",
			Help:      "Time spent resolving, scaling, and dialing service connections.",
			Buckets:   prometheus.DefBuckets,
		}, []string{"service_type", "result"}),
		activeConnections: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Namespace: "tearenv",
			Subsystem: "engine",
			Name:      "active_connections",
			Help:      "Current proxied service connections by service type.",
		}, []string{"service_type"}),
		scaleOperations: prometheus.NewCounterVec(prometheus.CounterOpts{
			Namespace: "tearenv",
			Subsystem: "engine",
			Name:      "scale_operations_total",
			Help:      "Total workload scale operations by direction and result.",
		}, []string{"direction", "result"}),
		scaleOperationDuration: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Namespace: "tearenv",
			Subsystem: "engine",
			Name:      "scale_operation_duration_seconds",
			Help:      "Time spent changing workload replica counts.",
			Buckets:   prometheus.DefBuckets,
		}, []string{"direction", "result"}),
		managedWorkloads: prometheus.NewGauge(prometheus.GaugeOpts{
			Namespace: "tearenv",
			Subsystem: "engine",
			Name:      "managed_workloads",
			Help:      "Current workloads scaled above zero by this daemon.",
		}),
	}

	collectors := []prometheus.Collector{
		metrics.ready,
		metrics.authenticationAttempts,
		metrics.handshakeFailures,
		metrics.catalogRequests,
		metrics.environmentProvisions,
		metrics.environmentDuration,
		metrics.serviceOpens,
		metrics.serviceOpenDuration,
		metrics.activeConnections,
		metrics.scaleOperations,
		metrics.scaleOperationDuration,
		metrics.managedWorkloads,
	}
	for _, collector := range collectors {
		if err := registerer.Register(collector); err != nil {
			return nil, fmt.Errorf("register Prometheus collector: %w", err)
		}
	}
	return metrics, nil
}

// SetReady publishes whether the daemon is ready to accept SSH connections.
func (metrics *Metrics) SetReady(ready bool) {
	if metrics == nil {
		return
	}
	if ready {
		metrics.ready.Set(1)
		return
	}
	metrics.ready.Set(0)
}

func (metrics *Metrics) observeAuthentication(method authorization.Method, result string) {
	if metrics != nil {
		metrics.authenticationAttempts.WithLabelValues(string(method), result).Inc()
	}
}

func (metrics *Metrics) observeHandshakeFailure() {
	if metrics != nil {
		metrics.handshakeFailures.Inc()
	}
}

func (metrics *Metrics) observeCatalog(result string) {
	if metrics != nil {
		metrics.catalogRequests.WithLabelValues(result).Inc()
	}
}

func (metrics *Metrics) observeEnvironmentProvision(result string, duration time.Duration) {
	if metrics != nil {
		metrics.environmentProvisions.WithLabelValues(result).Inc()
		metrics.environmentDuration.WithLabelValues(result).Observe(duration.Seconds())
	}
}

func (metrics *Metrics) observeServiceOpen(serviceType, result string, duration time.Duration) {
	if metrics != nil {
		metrics.serviceOpens.WithLabelValues(serviceType, result).Inc()
		metrics.serviceOpenDuration.WithLabelValues(serviceType, result).Observe(duration.Seconds())
	}
}

func (metrics *Metrics) connectionOpened(serviceType string) {
	if metrics != nil {
		metrics.activeConnections.WithLabelValues(serviceType).Inc()
	}
}

func (metrics *Metrics) connectionClosed(serviceType string) {
	if metrics != nil {
		metrics.activeConnections.WithLabelValues(serviceType).Dec()
	}
}

func (metrics *Metrics) observeScale(direction, result string, duration time.Duration) {
	if metrics != nil {
		metrics.scaleOperations.WithLabelValues(direction, result).Inc()
		metrics.scaleOperationDuration.WithLabelValues(direction, result).Observe(duration.Seconds())
	}
}

func (metrics *Metrics) workloadStarted() {
	if metrics != nil {
		metrics.managedWorkloads.Inc()
	}
}

func (metrics *Metrics) workloadStopped() {
	if metrics != nil {
		metrics.managedWorkloads.Dec()
	}
}
