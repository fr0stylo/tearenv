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
	"github.com/fr0stylo/tearenv/internal/kube"
	"github.com/fr0stylo/tearenv/internal/scaler"
	"github.com/fr0stylo/tearenv/internal/server"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/spf13/cobra"
	"golang.org/x/crypto/ssh"
)

const (
	defaultListenAddress  = ":2222"
	defaultMetricsAddress = ":9090"
	defaultHostKeyPath    = ".data/ssh_host_ed25519_key"
	defaultUsersPath      = ".data/users.json"
	defaultBlueprintName  = "developer-environment"
)

type grantOptions struct {
	usersPath         string
	identity          string
	name              string
	target            string
	localPort         uint
	workloadKind      string
	workloadNamespace string
	workloadName      string
	replicas          int
	readyTimeout      time.Duration
	idleTimeout       time.Duration
}

type inviteOptions struct {
	usersPath string
	identity  string
}

type blueprintInitOptions struct {
	name string
}

type serveOptions struct {
	listenAddress      string
	metricsAddress     string
	hostKeyPath        string
	usersPath          string
	authorizedKeysPath string
	scalerName         string
	kubernetes         bool
}

func main() {
	if err := run(os.Args[1:]); err != nil {
		slog.Error("tearenvd stopped", "error", err)
		os.Exit(1)
	}
}

func run(arguments []string) error {
	command := newRootCommand()
	command.SetArgs(arguments)
	return command.Execute()
}

func newRootCommand() *cobra.Command {
	command := &cobra.Command{
		Use:           "tearenvd",
		Short:         "Run and administer the tearenv service gateway",
		SilenceErrors: true,
	}
	command.AddCommand(
		newServeCommand(),
		newInviteCommand(),
		newServiceCommand(),
		newBlueprintCommand(),
	)
	return command
}

func newBlueprintCommand() *cobra.Command {
	command := &cobra.Command{
		Use:   "blueprint",
		Short: "Manage team environment blueprints",
	}
	command.AddCommand(newBlueprintInitCommand())
	return command
}

func newBlueprintInitCommand() *cobra.Command {
	options := blueprintInitOptions{name: defaultBlueprintName}
	command := &cobra.Command{
		Use:   "init",
		Short: "Write a starter environment blueprint",
		Args:  cobra.NoArgs,
		RunE: func(command *cobra.Command, _ []string) error {
			command.SilenceUsage = true
			return initializeBlueprint(options, command.OutOrStdout())
		},
	}
	command.Flags().StringVar(&options.name, "name", options.name, "blueprint metadata name")
	return command
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

func newServiceCommand() *cobra.Command {
	command := &cobra.Command{
		Use:   "service",
		Short: "Manage identity-bound services",
	}
	command.AddCommand(newGrantCommand())
	return command
}

func newGrantCommand() *cobra.Command {
	options := grantOptions{
		usersPath:    defaultUsersPath,
		replicas:     1,
		readyTimeout: 2 * time.Minute,
		idleTimeout:  10 * time.Minute,
	}
	command := &cobra.Command{
		Use:   "grant",
		Short: "Grant an identity access to a named service",
		Args:  cobra.NoArgs,
		RunE: func(command *cobra.Command, _ []string) error {
			command.SilenceUsage = true
			return grantService(options)
		},
	}
	flags := command.Flags()
	flags.StringVar(&options.usersPath, "users", options.usersPath, "authentication credentials and access policy JSON file")
	flags.StringVar(&options.identity, "identity", "", "developer identity receiving access")
	flags.StringVar(&options.name, "name", "", "client-visible service name")
	flags.StringVar(&options.target, "target", "", "server-side service DNS name and port")
	flags.UintVar(&options.localPort, "local-port", 0, "suggested client-side port; defaults to target port")
	flags.StringVar(&options.workloadKind, "workload-kind", "", "scaler-specific workload kind (Kubernetes: deployment or statefulset)")
	flags.StringVar(&options.workloadNamespace, "workload-namespace", "", "scaler-specific workload namespace, if applicable")
	flags.StringVar(&options.workloadName, "workload-name", "", "scaler-specific workload name")
	flags.IntVar(&options.replicas, "replicas", options.replicas, "replicas to start on first connection")
	flags.DurationVar(&options.readyTimeout, "ready-timeout", options.readyTimeout, "maximum service startup wait")
	flags.DurationVar(&options.idleTimeout, "idle-timeout", options.idleTimeout, "idle period before scaling to zero")
	mustMarkRequired(command, "identity", "name", "target")
	return command
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

func newInviteCommand() *cobra.Command {
	options := inviteOptions{usersPath: defaultUsersPath}
	command := &cobra.Command{
		Use:   "invite",
		Short: "Create a one-time enrollment invite",
		Args:  cobra.NoArgs,
		RunE: func(command *cobra.Command, _ []string) error {
			command.SilenceUsage = true
			return createInvite(options, command.OutOrStdout())
		},
	}
	flags := command.Flags()
	flags.StringVar(&options.usersPath, "users", options.usersPath, "authentication credentials and access policy JSON file")
	flags.StringVar(&options.identity, "identity", "", "developer identity to invite")
	mustMarkRequired(command, "identity")
	return command
}

func createInvite(options inviteOptions, output io.Writer) error {
	invite, err := authorization.CreateInvite(options.usersPath, options.identity)
	if err != nil {
		return fmt.Errorf("create invite: %w", err)
	}
	fmt.Fprintln(output, invite)
	return nil
}

func newServeCommand() *cobra.Command {
	options := serveOptions{
		listenAddress:  defaultListenAddress,
		metricsAddress: defaultMetricsAddress,
		hostKeyPath:    defaultHostKeyPath,
		usersPath:      defaultUsersPath,
	}
	command := &cobra.Command{
		Use:   "serve",
		Short: "Run the SSH service gateway",
		Args:  cobra.NoArgs,
		RunE: func(command *cobra.Command, _ []string) error {
			command.SilenceUsage = true
			return serve(command.Context(), options)
		},
	}
	flags := command.Flags()
	flags.StringVar(&options.listenAddress, "listen", options.listenAddress, "SSH listen address")
	flags.StringVar(&options.metricsAddress, "metrics-listen", options.metricsAddress, "Prometheus metrics listen address; empty disables metrics")
	flags.StringVar(&options.hostKeyPath, "host-key", options.hostKeyPath, "persistent SSH host private key")
	flags.StringVar(&options.usersPath, "users", options.usersPath, "authentication credentials and access policy JSON file")
	flags.StringVar(&options.authorizedKeysPath, "authorized-keys", "", "identity-bound SSH public keys JSON file (for example, a mounted Kubernetes Secret)")
	flags.StringVar(&options.scalerName, "scaler", "", "workload scaler backend (supported: kubernetes)")
	flags.BoolVar(&options.kubernetes, "kubernetes", false, "deprecated alias for --scaler kubernetes")
	if err := flags.MarkDeprecated("kubernetes", "use --scaler kubernetes instead"); err != nil {
		panic(err)
	}
	return command
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
	gateway := server.NewLifecycleGateway(credentials, backend, slog.Default(), server.WithMetrics(metrics))
	tunnelServer, err := server.New(server.Config{
		Authenticator: authenticator,
		Enrollment:    credentials,
		Policy:        credentials,
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

func mustMarkRequired(command *cobra.Command, names ...string) {
	for _, name := range names {
		if err := command.MarkFlagRequired(name); err != nil {
			panic(err)
		}
	}
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
