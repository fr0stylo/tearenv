package main

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"os"
	"os/signal"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"syscall"

	v1alpha1 "github.com/fr0stylo/tearenv/api/v1alpha1"
	"github.com/fr0stylo/tearenv/internal/client"
	"github.com/fr0stylo/tearenv/internal/profile"
	"github.com/fr0stylo/tearenv/internal/protocol"
	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/knownhosts"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func main() {
	if err := run(os.Args[1:]); err != nil {
		slog.Error("tearenv stopped", "error", err)
		os.Exit(1)
	}
}

func login(ctx context.Context, options loginOptions, input io.Reader, output, prompts io.Writer) error {
	identity, err := loginIdentity(options.identity, options.identityDefault, input, prompts)
	if err != nil {
		return err
	}
	signer, err := client.LoadOrCreatePrivateKey(options.privateKeyPath, identity)
	if err != nil {
		return err
	}
	registration := v1alpha1.UserRegistration{
		TypeMeta: metav1.TypeMeta{APIVersion: v1alpha1.APIVersion, Kind: v1alpha1.UserRegistrationKind},
		ObjectMeta: metav1.ObjectMeta{
			Name:      v1alpha1.ResourceName(identity),
			Namespace: options.namespace,
		},
		Spec: v1alpha1.UserRegistrationSpec{
			Identity: identity,
			PublicKeys: []v1alpha1.SSHPublicKey{{
				Name: v1alpha1.KeyName(options.identityDefault),
				Key:  strings.TrimSpace(string(ssh.MarshalAuthorizedKey(signer.PublicKey()))),
			}},
		},
	}
	contents, err := v1alpha1.MarshalUserRegistration(registration)
	if err != nil {
		return fmt.Errorf("create user registration: %w", err)
	}
	if err := saveProtectedFile(options.registrationPath, contents); err != nil {
		return fmt.Errorf("save user registration: %w", err)
	}
	accepted, err := client.SubmitUserRegistration(ctx, options.httpClient, options.apiURL, registration)
	if err != nil {
		if accepted.APIVersion != "" {
			if acceptedContents, marshalErr := v1alpha1.MarshalUserRegistration(accepted); marshalErr == nil {
				_ = saveProtectedFile(options.registrationPath, acceptedContents)
			}
		}
		return fmt.Errorf("%w; registration saved at %q", err, options.registrationPath)
	}
	contents, err = v1alpha1.MarshalUserRegistration(accepted)
	if err != nil {
		return fmt.Errorf("encode accepted user registration: %w", err)
	}
	if err := saveProtectedFile(options.registrationPath, contents); err != nil {
		return fmt.Errorf("save accepted user registration: %w", err)
	}
	saved := profile.Profile{
		ServerAddress: options.serverAddress,
		Identity:      identity,
		PrivateKey:    options.privateKeyPath,
		KnownHosts:    options.knownHostsPath,
		Insecure:      options.insecure,
	}
	if err := profile.Save(options.profilePath, saved); err != nil {
		return fmt.Errorf("save login: %w", err)
	}
	slog.Info("login prepared", "identity", identity, "config", options.profilePath, "registration", options.registrationPath)
	_, err = fmt.Fprintln(output, options.registrationPath)
	return err
}

func loginIdentity(value, fallback string, input io.Reader, prompts io.Writer) (string, error) {
	if identity := strings.TrimSpace(value); identity != "" {
		return identity, nil
	}
	if fallback == "" {
		fallback = client.DefaultIdentity
	}
	if _, err := fmt.Fprintf(prompts, "Identity [%s]: ", fallback); err != nil {
		return "", fmt.Errorf("write identity prompt: %w", err)
	}
	line, err := bufio.NewReader(input).ReadString('\n')
	if err != nil && !errors.Is(err, io.EOF) {
		return "", fmt.Errorf("read identity: %w", err)
	}
	identity := strings.TrimSpace(line)
	if identity == "" {
		identity = fallback
	}
	return identity, nil
}

func saveProtectedFile(path string, contents []byte) error {
	directory := filepath.Dir(path)
	if err := os.MkdirAll(directory, 0o700); err != nil {
		return fmt.Errorf("create directory: %w", err)
	}
	temporary, err := os.CreateTemp(directory, ".tearenv-registration-*")
	if err != nil {
		return fmt.Errorf("create temporary file: %w", err)
	}
	temporaryPath := temporary.Name()
	defer os.Remove(temporaryPath)
	if err := temporary.Chmod(0o600); err != nil {
		_ = temporary.Close()
		return fmt.Errorf("protect temporary file: %w", err)
	}
	if _, err := temporary.Write(contents); err != nil {
		_ = temporary.Close()
		return fmt.Errorf("write temporary file: %w", err)
	}
	if err := temporary.Sync(); err != nil {
		_ = temporary.Close()
		return fmt.Errorf("sync temporary file: %w", err)
	}
	if err := temporary.Close(); err != nil {
		return fmt.Errorf("close temporary file: %w", err)
	}
	if err := os.Rename(temporaryPath, path); err != nil {
		return fmt.Errorf("replace %q: %w", path, err)
	}
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
	hostname, err := os.Hostname()
	if err != nil || strings.TrimSpace(hostname) == "" {
		return client.DefaultIdentity
	}
	return hostname
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

func defaultRegistrationPath() string {
	directory, err := os.UserConfigDir()
	if err != nil {
		return ".tearenv_user_registration.yaml"
	}
	return filepath.Join(directory, "tearenv", "user-registration.yaml")
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
