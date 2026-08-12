package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"os"
	"os/signal"
	"os/user"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"syscall"

	"github.com/fr0stylo/tearenv/internal/client"
	"github.com/fr0stylo/tearenv/internal/kube"
	"github.com/fr0stylo/tearenv/internal/profile"
	"github.com/fr0stylo/tearenv/internal/protocol"
	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/knownhosts"
)

func main() {
	if err := run(os.Args[1:]); err != nil {
		slog.Error("tearenv stopped", "error", err)
		os.Exit(1)
	}
}

func login(ctx context.Context, options loginOptions) error {
	hostKey, err := hostKeyCallback(options.knownHostsPath, options.insecure)
	if err != nil {
		return err
	}
	ctx, stop := signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM)
	defer stop()
	saved := profile.Profile{
		ServerAddress: options.serverAddress,
		Identity:      options.identity,
		KnownHosts:    options.knownHostsPath,
		Insecure:      options.insecure,
	}
	switch strings.ToLower(strings.TrimSpace(options.method)) {
	case "token":
		token, err := client.Enroll(ctx, client.EnrollmentConfig{
			ServerAddress: options.serverAddress,
			Identity:      options.identity,
			Invite:        options.invite,
			HostKey:       hostKey,
		})
		if err != nil {
			return err
		}
		saved.Token = token
	case "kubernetes":
		signer, err := client.LoadOrCreatePrivateKey(options.privateKeyPath, options.identity)
		if err != nil {
			return err
		}
		if err := kube.RegisterAuthorizedKey(ctx, kube.AuthorizedKeyOptions{
			Kubeconfig: options.kubeconfig,
			Context:    options.kubeContext,
			Namespace:  options.kubeNamespace,
			Secret:     options.kubeSecret,
			Identity:   options.identity,
		}, signer.PublicKey()); err != nil {
			return fmt.Errorf("register public key: %w", err)
		}
		saved.PrivateKey = options.privateKeyPath
	default:
		return fmt.Errorf("unsupported login method %q", options.method)
	}
	if err := profile.Save(options.profilePath, saved); err != nil {
		return fmt.Errorf("save login: %w", err)
	}
	slog.Info("login saved", "identity", options.identity, "server", options.serverAddress, "config", options.profilePath)
	return nil
}

func services(ctx context.Context, options servicesOptions, output io.Writer) error {
	saved, err := profile.Load(options.profilePath)
	if err != nil {
		return fmt.Errorf("load login; run 'tearenv login' first: %w", err)
	}
	hostKey, err := hostKeyCallback(saved.KnownHosts, saved.Insecure)
	if err != nil {
		return err
	}
	clientConfig, err := serviceClientConfig(saved, hostKey)
	if err != nil {
		return err
	}
	catalog, err := client.ListServices(ctx, clientConfig)
	if err != nil {
		return err
	}
	for _, service := range catalog {
		fmt.Fprintf(output, "%s\t127.0.0.1:%d\n", service.Name, service.LocalPort)
	}
	return nil
}

func connect(ctx context.Context, options connectOptions, specifications []string) error {
	saved, err := profile.Load(options.profilePath)
	if err != nil {
		return fmt.Errorf("load login; run 'tearenv login' first: %w", err)
	}
	if options.serverAddress != "" {
		saved.ServerAddress = options.serverAddress
	}
	if options.identity != "" {
		saved.Identity = options.identity
	}
	if options.token != "" {
		saved.Token = options.token
	}
	if options.privateKeyPath != "" {
		saved.PrivateKey = options.privateKeyPath
	}
	if options.knownHostsPath != "" {
		saved.KnownHosts = options.knownHostsPath
	}
	if options.insecure {
		saved.Insecure = true
	}
	hostKey, err := hostKeyCallback(saved.KnownHosts, saved.Insecure)
	if err != nil {
		return err
	}
	clientConfig, err := serviceClientConfig(saved, hostKey)
	if err != nil {
		return err
	}
	ctx, stop := signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM)
	defer stop()
	catalog, err := client.ListServices(ctx, clientConfig)
	if err != nil {
		return err
	}
	requested, err := selectServices(catalog, specifications, options.listenHost)
	if err != nil {
		return err
	}
	return client.RunServices(ctx, clientConfig, requested, nil)
}

func selectServices(catalog []protocol.Service, specifications []string, listenHost string) ([]client.LocalService, error) {
	available := make(map[string]protocol.Service, len(catalog))
	for _, service := range catalog {
		available[service.Name] = service
	}
	if len(specifications) == 0 {
		specifications = make([]string, 0, len(catalog))
		for name := range available {
			specifications = append(specifications, name)
		}
		sort.Strings(specifications)
	}
	selected := make([]client.LocalService, 0, len(specifications))
	for _, specification := range specifications {
		name, listenAddress, overridden := strings.Cut(specification, "=")
		service, exists := available[name]
		if !exists {
			return nil, fmt.Errorf("service %q is not granted to this identity", name)
		}
		if !overridden {
			listenAddress = net.JoinHostPort(listenHost, strconv.Itoa(int(service.LocalPort)))
		}
		selected = append(selected, client.LocalService{Name: name, ListenAddress: listenAddress})
	}
	if len(selected) == 0 {
		return nil, errors.New("no services are granted to this identity")
	}
	return selected, nil
}

func defaultIdentity() string {
	current, err := user.Current()
	if err != nil {
		return client.DefaultIdentity
	}
	return current.Username
}

func hostKeyCallback(path string, insecure bool) (ssh.HostKeyCallback, error) {
	if insecure {
		slog.Warn("SSH host key verification is disabled")
		return ssh.InsecureIgnoreHostKey(), nil //nolint:gosec -- explicit development flag
	}
	if path == "" {
		return nil, errors.New("known-hosts path is required")
	}
	callback, err := knownhosts.New(path)
	if err != nil {
		return nil, fmt.Errorf("load known_hosts: %w", err)
	}
	return callback, nil
}

func defaultKnownHostsPath() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".ssh", "known_hosts")
}

func defaultProfilePath() string {
	directory, err := os.UserConfigDir()
	if err != nil {
		return ".tearenv.json"
	}
	return filepath.Join(directory, "tearenv", "config.json")
}

func defaultPrivateKeyPath() string {
	directory, err := os.UserConfigDir()
	if err != nil {
		return ".tearenv_id_ed25519"
	}
	return filepath.Join(directory, "tearenv", "id_ed25519")
}

func serviceClientConfig(saved *profile.Profile, hostKey ssh.HostKeyCallback) (client.ServiceClientConfig, error) {
	config := client.ServiceClientConfig{
		ServerAddress: saved.ServerAddress,
		Identity:      saved.Identity,
		Token:         saved.Token,
		HostKey:       hostKey,
	}
	if saved.PrivateKey != "" {
		signer, err := client.LoadPrivateKey(saved.PrivateKey)
		if err != nil {
			return client.ServiceClientConfig{}, err
		}
		config.Signer = signer
	}
	return config, nil
}
