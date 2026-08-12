package server

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/fr0stylo/tearenv/internal/authorization"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"
)

func TestMetricsRecordsDaemonEvents(t *testing.T) {
	registry := prometheus.NewRegistry()
	metrics, err := NewMetrics(registry)
	if err != nil {
		t.Fatal(err)
	}

	metrics.observeAuthentication(authorization.MethodPassword, metricResultSuccess)
	metrics.observeAuthentication(authorization.MethodPassword, metricResultRejected)
	metrics.observeCatalog(metricResultSuccess)
	metrics.observeEnvironmentProvision(metricResultSuccess, 20*time.Millisecond)
	metrics.observeHandshakeFailure()

	if got := testutil.ToFloat64(metrics.authenticationAttempts.WithLabelValues("password", metricResultSuccess)); got != 1 {
		t.Fatalf("successful authentication attempts = %v, want 1", got)
	}
	if got := testutil.ToFloat64(metrics.authenticationAttempts.WithLabelValues("password", metricResultRejected)); got != 1 {
		t.Fatalf("rejected authentication attempts = %v, want 1", got)
	}
	if got := testutil.ToFloat64(metrics.catalogRequests.WithLabelValues(metricResultSuccess)); got != 1 {
		t.Fatalf("successful catalog requests = %v, want 1", got)
	}
	if got := testutil.ToFloat64(metrics.environmentProvisions.WithLabelValues(metricResultSuccess)); got != 1 {
		t.Fatalf("successful environment provisions = %v, want 1", got)
	}
	if got := testutil.ToFloat64(metrics.handshakeFailures); got != 1 {
		t.Fatalf("handshake failures = %v, want 1", got)
	}
}

func TestMetricsRejectsDuplicateRegistration(t *testing.T) {
	registry := prometheus.NewRegistry()
	if _, err := NewMetrics(registry); err != nil {
		t.Fatal(err)
	}
	if _, err := NewMetrics(registry); err == nil {
		t.Fatal("NewMetrics() succeeded with collectors already registered")
	}
}

func TestInstrumentedGatewayRecordsDeniedOpen(t *testing.T) {
	registry := prometheus.NewRegistry()
	metrics, err := NewMetrics(registry)
	if err != nil {
		t.Fatal(err)
	}
	gateway := NewLifecycleGateway(denyingPolicy{}, nil, nil, WithMetrics(metrics))

	_, _, err = gateway.Open(context.Background(), "alice", "postgres")
	if !errors.Is(err, ErrServiceDenied) {
		t.Fatalf("Open() error = %v, want ErrServiceDenied", err)
	}
	if got := testutil.ToFloat64(metrics.serviceOpens.WithLabelValues(metricServiceUnknown, metricResultDenied)); got != 1 {
		t.Fatalf("denied open attempts = %v, want 1", got)
	}
}

func TestMetricsRecordsScalingAndConnections(t *testing.T) {
	registry := prometheus.NewRegistry()
	metrics, err := NewMetrics(registry)
	if err != nil {
		t.Fatal(err)
	}

	metrics.observeScale(metricDirectionUp, metricResultSuccess, 10*time.Millisecond)
	metrics.workloadStarted()
	metrics.connectionOpened(metricServiceManaged)
	metrics.connectionClosed(metricServiceManaged)
	metrics.workloadStopped()

	if got := testutil.ToFloat64(metrics.scaleOperations.WithLabelValues(metricDirectionUp, metricResultSuccess)); got != 1 {
		t.Fatalf("successful scale-up operations = %v, want 1", got)
	}
	if got := testutil.ToFloat64(metrics.activeConnections.WithLabelValues(metricServiceManaged)); got != 0 {
		t.Fatalf("active managed connections = %v, want 0", got)
	}
	if got := testutil.ToFloat64(metrics.managedWorkloads); got != 0 {
		t.Fatalf("managed workloads = %v, want 0", got)
	}
}

type denyingPolicy struct{}

func (denyingPolicy) Services(string) ([]authorization.Service, error) {
	return nil, nil
}

func (denyingPolicy) ResolveService(string, string) (authorization.Service, bool) {
	return authorization.Service{}, false
}
