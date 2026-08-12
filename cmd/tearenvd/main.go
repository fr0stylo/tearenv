package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/fr0stylo/tearenv/internal/authorization"
	"github.com/fr0stylo/tearenv/internal/blueprint"
	"github.com/fr0stylo/tearenv/internal/environment"
	"github.com/fr0stylo/tearenv/internal/kube"
	"github.com/fr0stylo/tearenv/internal/scaler"
	"github.com/fr0stylo/tearenv/internal/server"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"golang.org/x/crypto/ssh"
)

func main() {
	if err := run(os.Args[1:]); err != nil {
		slog.Error("tearenvd stopped", "error", err)
		os.Exit(1)
	}
}

func initializeBlueprint(options blueprintInitOptions, output io.Writer) error {
	contents, err := blueprint.Marshal(blueprint.Default(options.name))
	if err != nil {
		return fmt.Errorf("initialize blueprint: %w", err)
	}
	if _, err := output.Write(contents); err != nil {
		return fmt.Errorf("write blueprint: %w", err)
	}
	return nil
}

func grantService(options grantOptions) error {
	if options.localPort > 65535 {
		return fmt.Errorf("local port %d is invalid", options.localPort)
	}
	if options.replicas < 1 || int64(options.replicas) > 2147483647 {
		return fmt.Errorf("replicas %d is invalid", options.replicas)
	}
	service := authorization.Service{
		Name:      options.name,
		Target:    options.target,
		LocalPort: uint32(options.localPort),
	}
	if options.workloadKind != "" {
		service.Workload = &authorization.Workload{
			Kind:         options.workloadKind,
			Namespace:    options.workloadNamespace,
			Name:         options.workloadName,
			Replicas:     int32(options.replicas),
			ReadyTimeout: options.readyTimeout,
			IdleTimeout:  options.idleTimeout,
		}
	}
	if err := authorization.GrantService(options.usersPath, options.identity, service); err != nil {
		return fmt.Errorf("grant service: %w", err)
	}
	slog.Info("service granted", "identity", options.identity, "service", options.name, "target", options.target)
	return nil
}

func createInvite(options inviteOptions, output io.Writer) error {
	invite, err := authorization.CreateInvite(options.usersPath, options.identity)
	if err != nil {
		return fmt.Errorf("create invite: %w", err)
	}
	fmt.Fprintln(output, invite)
	return nil
}

func serve(ctx context.Context, options serveOptions) error {
	metrics, metricsHandler, err := newMetricsEndpoint()
	if err != nil {
		return err
	}
	credentials, err := authorization.LoadCredentials(options.usersPath)
	if err != nil {
		return err
	}
	providers := []authorization.Authenticator{credentials}
	if options.authorizedKeysPath != "" {
		publicKeys, err := authorization.LoadPublicKeys(options.authorizedKeysPath)
		if err != nil {
			return err
		}
		providers = append(providers, publicKeys)
	}
	authenticator, err := authorization.NewChain(providers...)
	if err != nil {
		return err
	}
	signer, err := server.LoadOrCreateHostKey(options.hostKeyPath)
	if err != nil {
		return err
	}
	selectedScaler := strings.ToLower(strings.TrimSpace(options.scalerName))
	if options.kubernetes {
		if selectedScaler != "" && selectedScaler != "kubernetes" {
			return fmt.Errorf("--kubernetes conflicts with --scaler %s", selectedScaler)
		}
		selectedScaler = "kubernetes"
	}
	backend, err := newScalerBackend(selectedScaler)
	if err != nil {
		return err
	}
	var policy authorization.Policy = credentials
	var provisioner server.EnvironmentProvisioner
	if options.blueprintPath != "" {
		if selectedScaler != "kubernetes" {
			return errors.New("--blueprint requires --scaler kubernetes")
		}
		contents, err := os.ReadFile(options.blueprintPath)
		if err != nil {
			return fmt.Errorf("read environment blueprint %q: %w", options.blueprintPath, err)
		}
		document, err := blueprint.Load(contents)
		if err != nil {
			return fmt.Errorf("load environment blueprint %q: %w", options.blueprintPath, err)
		}
		applier, err := kube.NewInClusterBlueprintApplier()
		if err != nil {
			return fmt.Errorf("configure Kubernetes blueprint provisioner: %w", err)
		}
		manager, err := environment.NewManager(document, applier, credentials)
		if err != nil {
			return fmt.Errorf("configure environment manager: %w", err)
		}
		policy = manager
		provisioner = manager
	}
	gateway := server.NewLifecycleGateway(policy, backend, slog.Default(), server.WithMetrics(metrics))
	tunnelServer, err := server.New(server.Config{
		Authenticator: authenticator,
		Enrollment:    credentials,
		Policy:        policy,
		Provisioner:   provisioner,
		Signer:        signer,
		Gateway:       gateway,
		Metrics:       metrics,
	})
	if err != nil {
		return fmt.Errorf("configure server: %w", err)
	}
	listener, err := net.Listen("tcp", options.listenAddress)
	if err != nil {
		return fmt.Errorf("listen on %s: %w", options.listenAddress, err)
	}

	ctx, stop := signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM)
	defer stop()
	if options.metricsAddress == "" {
		metrics.SetReady(true)
		defer metrics.SetReady(false)
		return serveSSH(ctx, tunnelServer, listener, signer, options, selectedScaler, "disabled")
	}
	metricsListener, err := net.Listen("tcp", options.metricsAddress)
	if err != nil {
		_ = listener.Close()
		return fmt.Errorf("listen for metrics on %s: %w", options.metricsAddress, err)
	}
	metricsServer := &http.Server{
		Handler:           metricsHandler,
		ReadHeaderTimeout: 5 * time.Second,
		IdleTimeout:       30 * time.Second,
	}
	metrics.SetReady(true)
	defer metrics.SetReady(false)
	slog.Info("tearenvd ready",
		"ssh", listener.Addr(),
		"metrics", metricsListener.Addr(),
		"users", options.usersPath,
		"authorized_keys", options.authorizedKeysPath,
		"blueprint", options.blueprintPath,
		"scaler", selectedScaler,
		"host_key_fingerprint", ssh.FingerprintSHA256(signer.PublicKey()),
	)
	return serveListeners(ctx, tunnelServer, listener, metricsServer, metricsListener)
}

func newMetricsEndpoint() (*server.Metrics, http.Handler, error) {
	registry := prometheus.NewRegistry()
	for _, collector := range []prometheus.Collector{
		collectors.NewGoCollector(),
		collectors.NewProcessCollector(collectors.ProcessCollectorOpts{}),
	} {
		if err := registry.Register(collector); err != nil {
			return nil, nil, fmt.Errorf("register automatic Prometheus collector: %w", err)
		}
	}
	metrics, err := server.NewMetrics(registry)
	if err != nil {
		return nil, nil, err
	}
	metricsHandler := promhttp.InstrumentMetricHandler(registry, promhttp.HandlerFor(registry, promhttp.HandlerOpts{}))
	mux := http.NewServeMux()
	mux.Handle("/metrics", metricsHandler)
	return metrics, mux, nil
}

func serveSSH(
	ctx context.Context,
	tunnelServer *server.Server,
	listener net.Listener,
	signer ssh.Signer,
	options serveOptions,
	selectedScaler string,
	metricsAddress string,
) error {
	slog.Info("tearenvd ready",
		"ssh", listener.Addr(),
		"metrics", metricsAddress,
		"users", options.usersPath,
		"authorized_keys", options.authorizedKeysPath,
		"blueprint", options.blueprintPath,
		"scaler", selectedScaler,
		"host_key_fingerprint", ssh.FingerprintSHA256(signer.PublicKey()),
	)
	return tunnelServer.Serve(ctx, listener)
}

func serveListeners(
	ctx context.Context,
	tunnelServer *server.Server,
	sshListener net.Listener,
	metricsServer *http.Server,
	metricsListener net.Listener,
) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	errorsChannel := make(chan error, 2)
	go func() {
		errorsChannel <- tunnelServer.Serve(ctx, sshListener)
	}()
	go func() {
		errorsChannel <- serveMetrics(ctx, metricsServer, metricsListener)
	}()

	firstError := <-errorsChannel
	cancel()
	secondError := <-errorsChannel
	return errors.Join(firstError, secondError)
}

func serveMetrics(ctx context.Context, metricsServer *http.Server, listener net.Listener) error {
	done := make(chan struct{})
	defer close(done)
	go func() {
		select {
		case <-ctx.Done():
			shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			_ = metricsServer.Shutdown(shutdownCtx)
		case <-done:
		}
	}()
	err := metricsServer.Serve(listener)
	if errors.Is(err, http.ErrServerClosed) && ctx.Err() != nil {
		return nil
	}
	if err != nil {
		return fmt.Errorf("serve Prometheus metrics: %w", err)
	}
	return nil
}

func newScalerBackend(name string) (scaler.Backend, error) {
	switch name {
	case "":
		return nil, nil
	case "kubernetes":
		backend, err := kube.NewInClusterScaler()
		if err != nil {
			return nil, fmt.Errorf("configure Kubernetes scaler: %w", err)
		}
		return backend, nil
	default:
		return nil, fmt.Errorf("unsupported scaler backend %q", name)
	}
}
