package server

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"sync"
	"time"

	"github.com/fr0stylo/tearenv/internal/authorization"
	"github.com/fr0stylo/tearenv/internal/scaler"
)

var ErrScalerUnavailable = errors.New("service requires scaling but no scaler backend is configured")

type lifecycleGateway struct {
	policy   authorization.Policy
	scaler   scaler.Backend
	logger   *slog.Logger
	metrics  *Metrics
	mu       sync.Mutex
	runtimes map[string]*serviceRuntime
}

type serviceRuntime struct {
	mu        sync.Mutex
	workload  *authorization.Workload
	label     string
	active    int
	scaled    bool
	idleTimer *time.Timer
}

// LifecycleOption customizes the service lifecycle engine.
type LifecycleOption func(*lifecycleGateway)

// WithMetrics instruments service lifecycle operations with metrics.
func WithMetrics(metrics *Metrics) LifecycleOption {
	return func(gateway *lifecycleGateway) {
		gateway.metrics = metrics
	}
}

// NewLifecycleGateway creates the service runtime boundary. A nil scaler is
// valid for static services but rejects grants containing workload metadata.
func NewLifecycleGateway(
	policy authorization.Policy,
	backend scaler.Backend,
	logger *slog.Logger,
	options ...LifecycleOption,
) ServiceGateway {
	if logger == nil {
		logger = slog.Default()
	}
	gateway := &lifecycleGateway{
		policy:   policy,
		scaler:   backend,
		logger:   logger,
		runtimes: make(map[string]*serviceRuntime),
	}
	for _, option := range options {
		option(gateway)
	}
	return gateway
}

func (gateway *lifecycleGateway) Services(_ context.Context, identity string) ([]authorization.Service, error) {
	return gateway.policy.Services(identity)
}

// Shutdown cancels idle timers and scales down workloads started by this gateway.
func (gateway *lifecycleGateway) Shutdown(ctx context.Context) error {
	gateway.mu.Lock()
	runtimes := make([]*serviceRuntime, 0, len(gateway.runtimes))
	for _, runtime := range gateway.runtimes {
		runtimes = append(runtimes, runtime)
	}
	gateway.mu.Unlock()
	var firstError error
	for _, runtime := range runtimes {
		runtime.mu.Lock()
		if runtime.idleTimer != nil {
			runtime.idleTimer.Stop()
			runtime.idleTimer = nil
		}
		if runtime.scaled && runtime.workload != nil {
			if err := gateway.scale(ctx, runtime.workload, 0); err != nil && firstError == nil {
				firstError = err
			} else if err == nil {
				runtime.scaled = false
				gateway.metrics.workloadStopped()
			}
		}
		runtime.mu.Unlock()
	}
	return firstError
}

func (gateway *lifecycleGateway) Open(ctx context.Context, identity, name string) (net.Conn, authorization.Service, error) {
	started := time.Now()
	serviceType := metricServiceUnknown
	result := metricResultSuccess
	defer func() {
		gateway.metrics.observeServiceOpen(serviceType, result, time.Since(started))
	}()

	service, allowed := gateway.policy.ResolveService(identity, name)
	if !allowed {
		result = metricResultDenied
		return nil, authorization.Service{}, ErrServiceDenied
	}
	serviceType = metricServiceStatic
	if service.Workload != nil {
		serviceType = metricServiceManaged
	}
	runtime := gateway.runtime(identity, service)
	runtime.mu.Lock()
	defer runtime.mu.Unlock()
	if service.Workload != nil {
		workload := *service.Workload
		runtime.workload = &workload
	}
	runtime.label = identity + "/" + service.Name
	if runtime.idleTimer != nil {
		runtime.idleTimer.Stop()
		runtime.idleTimer = nil
	}
	if service.Workload != nil && !runtime.scaled {
		if gateway.scaler == nil {
			result = metricResultScalerUnavailable
			return nil, service, ErrScalerUnavailable
		}
		gateway.logger.Info("scaling service up", "identity", identity, "service", name, "replicas", service.Workload.Replicas)
		if err := gateway.scale(ctx, service.Workload, service.Workload.Replicas); err != nil {
			result = metricResultScaleError
			return nil, service, fmt.Errorf("scale service up: %w", err)
		}
		runtime.scaled = true
		gateway.metrics.workloadStarted()
	}

	connection, err := dialService(ctx, service)
	if err != nil {
		result = metricResultDialError
		if service.Workload != nil && runtime.active == 0 {
			downscaleCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			downscaleErr := gateway.scale(downscaleCtx, service.Workload, 0)
			cancel()
			if downscaleErr == nil {
				runtime.scaled = false
				gateway.metrics.workloadStopped()
			}
		}
		return nil, service, err
	}
	runtime.active++
	gateway.metrics.connectionOpened(serviceType)
	return &trackedConnection{
		Conn:    connection,
		release: func() { gateway.release(runtime, serviceType) },
	}, service, nil
}

func (gateway *lifecycleGateway) runtime(identity string, service authorization.Service) *serviceRuntime {
	key := identity + "\x00" + service.Name
	if service.Workload != nil {
		key = "workload\x00" + service.Workload.Kind + "\x00" + service.Workload.Namespace + "\x00" + service.Workload.Name
	}
	gateway.mu.Lock()
	defer gateway.mu.Unlock()
	runtime := gateway.runtimes[key]
	if runtime == nil {
		runtime = &serviceRuntime{}
		gateway.runtimes[key] = runtime
	}
	return runtime
}

func (gateway *lifecycleGateway) release(runtime *serviceRuntime, serviceType string) {
	runtime.mu.Lock()
	defer runtime.mu.Unlock()
	if runtime.active > 0 {
		runtime.active--
		gateway.metrics.connectionClosed(serviceType)
	}
	if runtime.active != 0 || runtime.workload == nil || !runtime.scaled {
		return
	}
	delay := runtime.workload.IdleTimeout
	gateway.logger.Info("service idle", "service", runtime.label, "downscale_in", delay)
	runtime.idleTimer = time.AfterFunc(delay, func() {
		runtime.mu.Lock()
		defer runtime.mu.Unlock()
		if runtime.active != 0 || !runtime.scaled {
			return
		}
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		if err := gateway.scale(ctx, runtime.workload, 0); err != nil {
			gateway.logger.Warn("service downscale failed", "service", runtime.label, "error", err)
			return
		}
		runtime.scaled = false
		gateway.metrics.workloadStopped()
		runtime.idleTimer = nil
		gateway.logger.Info("service scaled down", "service", runtime.label)
	})
}

func (gateway *lifecycleGateway) scale(ctx context.Context, workload *authorization.Workload, replicas int32) error {
	direction := metricDirectionDown
	if replicas > 0 {
		direction = metricDirectionUp
	}
	started := time.Now()
	err := gateway.scaler.Scale(ctx, scalerTarget(workload), replicas)
	result := metricResultSuccess
	if err != nil {
		result = metricResultError
	}
	gateway.metrics.observeScale(direction, result, time.Since(started))
	return err
}

func dialService(ctx context.Context, service authorization.Service) (net.Conn, error) {
	if service.Workload == nil {
		connection, err := (&net.Dialer{}).DialContext(ctx, "tcp", service.Target)
		if err != nil {
			return nil, fmt.Errorf("dial service target: %w", err)
		}
		return connection, nil
	}
	readyCtx, cancel := context.WithTimeout(ctx, service.Workload.ReadyTimeout)
	defer cancel()
	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()
	var lastError error
	for {
		connection, err := (&net.Dialer{Timeout: time.Second}).DialContext(readyCtx, "tcp", service.Target)
		if err == nil {
			return connection, nil
		}
		lastError = err
		select {
		case <-readyCtx.Done():
			return nil, fmt.Errorf("wait for service readiness: %w (last dial: %v)", readyCtx.Err(), lastError)
		case <-ticker.C:
		}
	}
}

func scalerTarget(workload *authorization.Workload) scaler.Target {
	return scaler.Target{Kind: workload.Kind, Namespace: workload.Namespace, Name: workload.Name}
}

type trackedConnection struct {
	net.Conn
	once    sync.Once
	release func()
}

func (connection *trackedConnection) Close() error {
	err := connection.Conn.Close()
	connection.once.Do(connection.release)
	return err
}
