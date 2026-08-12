package server

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/fr0stylo/tearenv/internal/authorization"
	scalerpkg "github.com/fr0stylo/tearenv/internal/scaler"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"
)

func TestLifecycleGatewayScalesUpWaitsAndScalesDown(t *testing.T) {
	address := reserveTCPAddress(t)
	credentials := credentialsWithWorkload(t, address)
	scaler := &fakeScaler{onScaleUp: func() { startEchoAt(t, address) }}
	registry := prometheus.NewRegistry()
	metrics, err := NewMetrics(registry)
	if err != nil {
		t.Fatal(err)
	}
	gateway := NewLifecycleGateway(
		credentials,
		scaler,
		slog.New(slog.NewTextHandler(io.Discard, nil)),
		WithMetrics(metrics),
	)

	connection, _, err := gateway.Open(context.Background(), "alice", "postgres")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := connection.Write([]byte("ready")); err != nil {
		t.Fatal(err)
	}
	response := make([]byte, 5)
	if _, err := io.ReadFull(connection, response); err != nil {
		t.Fatal(err)
	}
	if err := connection.Close(); err != nil {
		t.Fatal(err)
	}
	if target := scaler.Target(); target != (scalerpkg.Target{Kind: "statefulset", Namespace: "dev-alice", Name: "postgres"}) {
		t.Fatalf("scaler target = %#v", target)
	}

	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if calls := scaler.Calls(); len(calls) == 2 && calls[0] == 1 && calls[1] == 0 {
			if got := testutil.ToFloat64(metrics.serviceOpens.WithLabelValues(metricServiceManaged, metricResultSuccess)); got != 1 {
				t.Fatalf("successful managed service opens = %v, want 1", got)
			}
			if got := testutil.ToFloat64(metrics.scaleOperations.WithLabelValues(metricDirectionUp, metricResultSuccess)); got != 1 {
				t.Fatalf("successful scale-up operations = %v, want 1", got)
			}
			if got := testutil.ToFloat64(metrics.scaleOperations.WithLabelValues(metricDirectionDown, metricResultSuccess)); got != 1 {
				t.Fatalf("successful scale-down operations = %v, want 1", got)
			}
			if got := testutil.ToFloat64(metrics.activeConnections.WithLabelValues(metricServiceManaged)); got != 0 {
				t.Fatalf("active managed connections = %v, want 0", got)
			}
			if got := testutil.ToFloat64(metrics.managedWorkloads); got != 0 {
				t.Fatalf("managed workloads = %v, want 0", got)
			}
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("scale calls = %v, want [1 0]", scaler.Calls())
}

func TestLifecycleGatewayDeniesAnotherIdentity(t *testing.T) {
	credentials := credentialsWithWorkload(t, "127.0.0.1:12345")
	gateway := NewLifecycleGateway(credentials, &fakeScaler{}, nil)
	_, _, err := gateway.Open(context.Background(), "bob", "postgres")
	if !errors.Is(err, ErrServiceDenied) {
		t.Fatalf("Open() error = %v, want ErrServiceDenied", err)
	}
}

func TestLifecycleGatewaySharesActivityAcrossAliases(t *testing.T) {
	address := reserveTCPAddress(t)
	path := filepath.Join(t.TempDir(), "users.json")
	if _, err := authorization.CreateInvite(path, "alice"); err != nil {
		t.Fatal(err)
	}
	workload := &authorization.Workload{
		Kind: "statefulset", Namespace: "dev-alice", Name: "database",
		Replicas: 1, ReadyTimeout: time.Second, IdleTimeout: 30 * time.Millisecond,
	}
	for _, name := range []string{"postgres", "grpc"} {
		if err := authorization.GrantService(path, "alice", authorization.Service{Name: name, Target: address, Workload: workload}); err != nil {
			t.Fatal(err)
		}
	}
	credentials, err := authorization.LoadCredentials(path)
	if err != nil {
		t.Fatal(err)
	}
	scaler := &fakeScaler{onScaleUp: func() { startEchoAt(t, address) }}
	gateway := NewLifecycleGateway(credentials, scaler, nil)
	postgres, _, err := gateway.Open(context.Background(), "alice", "postgres")
	if err != nil {
		t.Fatal(err)
	}
	grpc, _, err := gateway.Open(context.Background(), "alice", "grpc")
	if err != nil {
		t.Fatal(err)
	}
	_ = postgres.Close()
	time.Sleep(60 * time.Millisecond)
	if calls := scaler.Calls(); len(calls) != 1 || calls[0] != 1 {
		t.Fatalf("scale calls while alias active = %v, want [1]", calls)
	}
	_ = grpc.Close()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if calls := scaler.Calls(); len(calls) == 2 && calls[1] == 0 {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("scale calls = %v, want [1 0]", scaler.Calls())
}

func TestLifecycleGatewayDownscalesAfterReadinessTimeout(t *testing.T) {
	path := filepath.Join(t.TempDir(), "users.json")
	if _, err := authorization.CreateInvite(path, "alice"); err != nil {
		t.Fatal(err)
	}
	if err := authorization.GrantService(path, "alice", authorization.Service{
		Name: "postgres", Target: reserveTCPAddress(t),
		Workload: &authorization.Workload{
			Kind: "statefulset", Namespace: "dev-alice", Name: "postgres",
			Replicas: 1, ReadyTimeout: 20 * time.Millisecond, IdleTimeout: time.Second,
		},
	}); err != nil {
		t.Fatal(err)
	}
	credentials, err := authorization.LoadCredentials(path)
	if err != nil {
		t.Fatal(err)
	}
	scaler := &fakeScaler{}
	gateway := NewLifecycleGateway(credentials, scaler, nil)
	_, _, err = gateway.Open(context.Background(), "alice", "postgres")
	if err == nil {
		t.Fatal("Open() succeeded for an unavailable service")
	}
	if calls := scaler.Calls(); len(calls) != 2 || calls[0] != 1 || calls[1] != 0 {
		t.Fatalf("scale calls = %v, want [1 0]", calls)
	}
}

type fakeScaler struct {
	mu        sync.Mutex
	calls     []int32
	targets   []scalerpkg.Target
	onScaleUp func()
	once      sync.Once
}

func (scaler *fakeScaler) Scale(_ context.Context, target scalerpkg.Target, replicas int32) error {
	scaler.mu.Lock()
	scaler.calls = append(scaler.calls, replicas)
	scaler.targets = append(scaler.targets, target)
	scaler.mu.Unlock()
	if replicas > 0 && scaler.onScaleUp != nil {
		scaler.once.Do(scaler.onScaleUp)
	}
	return nil
}

func (scaler *fakeScaler) Target() scalerpkg.Target {
	scaler.mu.Lock()
	defer scaler.mu.Unlock()
	if len(scaler.targets) == 0 {
		return scalerpkg.Target{}
	}
	return scaler.targets[0]
}

func (scaler *fakeScaler) Calls() []int32 {
	scaler.mu.Lock()
	defer scaler.mu.Unlock()
	return append([]int32(nil), scaler.calls...)
}

func credentialsWithWorkload(t *testing.T, target string) *authorization.Credentials {
	t.Helper()
	path := filepath.Join(t.TempDir(), "users.json")
	if _, err := authorization.CreateInvite(path, "alice"); err != nil {
		t.Fatal(err)
	}
	if err := authorization.GrantService(path, "alice", authorization.Service{
		Name: "postgres", Target: target, LocalPort: 5432,
		Workload: &authorization.Workload{
			Kind: "statefulset", Namespace: "dev-alice", Name: "postgres",
			Replicas: 1, ReadyTimeout: time.Second, IdleTimeout: 20 * time.Millisecond,
		},
	}); err != nil {
		t.Fatal(err)
	}
	credentials, err := authorization.LoadCredentials(path)
	if err != nil {
		t.Fatal(err)
	}
	return credentials
}

func reserveTCPAddress(t *testing.T) string {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	address := listener.Addr().String()
	_ = listener.Close()
	return address
}

func startEchoAt(t *testing.T, address string) {
	t.Helper()
	listener, err := net.Listen("tcp", address)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = listener.Close() })
	go func() {
		for {
			connection, err := listener.Accept()
			if err != nil {
				return
			}
			go func() {
				defer connection.Close()
				_, _ = io.Copy(connection, connection)
			}()
		}
	}()
}
