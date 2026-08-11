package server

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"sync"
	"time"

	"github.com/fr0stylo/tearenv/internal/scaler"
)

var ErrScalerUnavailable = errors.New("service requires scaling but no scaler backend is configured")

type lifecycleGateway struct {
	credentials *Credentials
	scaler      scaler.Backend
	logger      *slog.Logger
	mu          sync.Mutex
	runtimes    map[string]*serviceRuntime
}

type serviceRuntime struct {
	mu        sync.Mutex
	workload  *Workload
	label     string
	active    int
	scaled    bool
	idleTimer *time.Timer
}

// NewLifecycleGateway creates the service runtime boundary. A nil scaler is
// valid for static services but rejects grants containing workload metadata.
func NewLifecycleGateway(credentials *Credentials, backend scaler.Backend, logger *slog.Logger) ServiceGateway {
	if logger == nil {
		logger = slog.Default()
	}
	return &lifecycleGateway{
		credentials: credentials,
		scaler:      backend,
		logger:      logger,
		runtimes:    make(map[string]*serviceRuntime),
	}
}

func (gateway *lifecycleGateway) Services(_ context.Context, identity string) ([]Service, error) {
	return gateway.credentials.Services(identity)
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
			if err := gateway.scaler.Scale(ctx, runtime.workload.scalerTarget(), 0); err != nil && firstError == nil {
				firstError = err
			} else if err == nil {
				runtime.scaled = false
			}
		}
		runtime.mu.Unlock()
	}
	return firstError
}

func (gateway *lifecycleGateway) Open(ctx context.Context, identity, name string) (net.Conn, Service, error) {
	service, allowed := gateway.credentials.ResolveService(identity, name)
	if !allowed {
		return nil, Service{}, ErrServiceDenied
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
			return nil, service, ErrScalerUnavailable
		}
		gateway.logger.Info("scaling service up", "identity", identity, "service", name, "replicas", service.Workload.Replicas)
		if err := gateway.scaler.Scale(ctx, service.Workload.scalerTarget(), service.Workload.Replicas); err != nil {
			return nil, service, fmt.Errorf("scale service up: %w", err)
		}
		runtime.scaled = true
	}

	connection, err := dialService(ctx, service)
	if err != nil {
		if service.Workload != nil && runtime.active == 0 {
			downscaleCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			_ = gateway.scaler.Scale(downscaleCtx, service.Workload.scalerTarget(), 0)
			cancel()
			runtime.scaled = false
		}
		return nil, service, err
	}
	runtime.active++
	return &trackedConnection{
		Conn:    connection,
		release: func() { gateway.release(runtime) },
	}, service, nil
}

func (gateway *lifecycleGateway) runtime(identity string, service Service) *serviceRuntime {
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

func (gateway *lifecycleGateway) release(runtime *serviceRuntime) {
	runtime.mu.Lock()
	defer runtime.mu.Unlock()
	if runtime.active > 0 {
		runtime.active--
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
		if err := gateway.scaler.Scale(ctx, runtime.workload.scalerTarget(), 0); err != nil {
			gateway.logger.Warn("service downscale failed", "service", runtime.label, "error", err)
			return
		}
		runtime.scaled = false
		runtime.idleTimer = nil
		gateway.logger.Info("service scaled down", "service", runtime.label)
	})
}

func dialService(ctx context.Context, service Service) (net.Conn, error) {
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
