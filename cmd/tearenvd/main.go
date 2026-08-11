package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/fr0stylo/tearenv/internal/kube"
	"github.com/fr0stylo/tearenv/internal/scaler"
	"github.com/fr0stylo/tearenv/internal/server"
	"golang.org/x/crypto/ssh"
)

const (
	defaultListenAddress = ":2222"
	defaultHostKeyPath   = ".data/ssh_host_ed25519_key"
	defaultUsersPath     = ".data/users.json"
)

func main() {
	if err := run(os.Args[1:]); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return
		}
		slog.Error("tearenvd stopped", "error", err)
		os.Exit(1)
	}
}

func run(arguments []string) error {
	if len(arguments) > 0 && arguments[0] == "invite" {
		return createInvite(arguments[1:])
	}
	if len(arguments) > 0 && arguments[0] == "serve" {
		arguments = arguments[1:]
	}
	if len(arguments) > 1 && arguments[0] == "service" && arguments[1] == "grant" {
		return grantService(arguments[2:])
	}
	return serve(arguments)
}

func grantService(arguments []string) error {
	flags := flag.NewFlagSet("tearenvd service grant", flag.ContinueOnError)
	usersPath := flags.String("users", defaultUsersPath, "per-user credentials and access JSON file")
	identity := flags.String("identity", "", "developer identity receiving access")
	name := flags.String("name", "", "client-visible service name")
	target := flags.String("target", "", "server-side service DNS name and port")
	localPort := flags.Uint("local-port", 0, "suggested client-side port; defaults to target port")
	workloadKind := flags.String("workload-kind", "", "scaler-specific workload kind (Kubernetes: deployment or statefulset)")
	workloadNamespace := flags.String("workload-namespace", "", "scaler-specific workload namespace, if applicable")
	workloadName := flags.String("workload-name", "", "scaler-specific workload name")
	replicas := flags.Int("replicas", 1, "replicas to start on first connection")
	readyTimeout := flags.Duration("ready-timeout", 2*time.Minute, "maximum service startup wait")
	idleTimeout := flags.Duration("idle-timeout", 10*time.Minute, "idle period before scaling to zero")
	if err := flags.Parse(arguments); err != nil {
		return err
	}
	if *localPort > 65535 {
		return fmt.Errorf("local port %d is invalid", *localPort)
	}
	if *replicas < 1 || int64(*replicas) > 2147483647 {
		return fmt.Errorf("replicas %d is invalid", *replicas)
	}
	service := server.Service{
		Name:      *name,
		Target:    *target,
		LocalPort: uint32(*localPort),
	}
	if *workloadKind != "" {
		service.Workload = &server.Workload{
			Kind: *workloadKind, Namespace: *workloadNamespace, Name: *workloadName,
			Replicas: int32(*replicas), ReadyTimeout: *readyTimeout, IdleTimeout: *idleTimeout,
		}
	}
	if err := server.GrantService(*usersPath, *identity, service); err != nil {
		return fmt.Errorf("grant service: %w", err)
	}
	slog.Info("service granted", "identity", *identity, "service", *name, "target", *target)
	return nil
}

func createInvite(arguments []string) error {
	flags := flag.NewFlagSet("tearenvd invite", flag.ContinueOnError)
	usersPath := flags.String("users", defaultUsersPath, "per-user credentials JSON file")
	identity := flags.String("identity", "", "developer identity to invite")
	if err := flags.Parse(arguments); err != nil {
		return err
	}
	invite, err := server.CreateInvite(*usersPath, *identity)
	if err != nil {
		return fmt.Errorf("create invite: %w", err)
	}
	fmt.Fprintln(os.Stdout, invite)
	return nil
}

func serve(arguments []string) error {
	flags := flag.NewFlagSet("tearenvd serve", flag.ContinueOnError)
	listenAddress := flags.String("listen", defaultListenAddress, "SSH listen address")
	hostKeyPath := flags.String("host-key", defaultHostKeyPath, "persistent SSH host private key")
	usersPath := flags.String("users", defaultUsersPath, "per-user credentials JSON file")
	scalerName := flags.String("scaler", "", "workload scaler backend (supported: kubernetes)")
	kubernetes := flags.Bool("kubernetes", false, "deprecated alias for -scaler kubernetes")
	if err := flags.Parse(arguments); err != nil {
		return err
	}

	credentials, err := server.LoadCredentials(*usersPath)
	if err != nil {
		return err
	}
	signer, err := server.LoadOrCreateHostKey(*hostKeyPath)
	if err != nil {
		return err
	}
	selectedScaler := strings.ToLower(strings.TrimSpace(*scalerName))
	if *kubernetes {
		if selectedScaler != "" && selectedScaler != "kubernetes" {
			return fmt.Errorf("-kubernetes conflicts with -scaler %s", selectedScaler)
		}
		selectedScaler = "kubernetes"
	}
	backend, err := newScalerBackend(selectedScaler)
	if err != nil {
		return err
	}
	gateway := server.NewLifecycleGateway(credentials, backend, slog.Default())
	tunnelServer, err := server.New(server.Config{
		Credentials: credentials,
		Signer:      signer,
		Gateway:     gateway,
	})
	if err != nil {
		return fmt.Errorf("configure server: %w", err)
	}
	listener, err := net.Listen("tcp", *listenAddress)
	if err != nil {
		return fmt.Errorf("listen on %s: %w", *listenAddress, err)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	slog.Info("tearenvd ready",
		"ssh", listener.Addr(),
		"users", *usersPath,
		"scaler", selectedScaler,
		"host_key_fingerprint", ssh.FingerprintSHA256(signer.PublicKey()),
	)
	return tunnelServer.Serve(ctx, listener)
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
