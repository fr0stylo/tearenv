package main

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"syscall"

	v1alpha1 "github.com/fr0stylo/tearenv/api/v1alpha1"
	"github.com/fr0stylo/tearenv/internal/authn"
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
	registrationToken, err := loadRegistrationToken(options.registrationTokenPath)
	if err != nil {
		return err
	}
	authentication := authn.Configuration{Mode: authn.MethodAnonymous}
	var discoveryErr error
	if options.discoverAuthentication {
		authentication, discoveryErr = client.DiscoverAuthentication(ctx, options.httpClient, options.apiURL)
	}
	if discoveryErr != nil {
		// Servers predating authentication discovery remain usable during migration.
		authentication.Mode = authn.MethodAnonymous
		if registrationToken != "" {
			authentication.Mode = authn.MethodToken
		}
	}
	var identity string
	var bearerToken string
	if authentication.Mode == authn.MethodOIDC {
		if registrationToken != "" {
			return errors.New("--registration-token-file cannot be used when the server requires OIDC")
		}
		if authentication.OIDC == nil {
			return errors.New("server advertised OIDC without client configuration")
		}
		oidcLogin, err := client.OIDCLogin(ctx, options.httpClient, *authentication.OIDC, client.OIDCLoginOptions{
			Device: options.oidcDevice, Output: prompts,
		})
		if err != nil {
			return err
		}
		identity = oidcLogin.Identity
		bearerToken = oidcLogin.SubjectToken
		if requested := strings.TrimSpace(options.identity); requested != "" && requested != identity {
			return fmt.Errorf("requested identity %q does not match OIDC identity %q", requested, identity)
		}
	} else {
		identity, err = loginIdentity(options.identity, options.identityDefault, input, prompts)
		if err != nil {
			return err
		}
		bearerToken = registrationToken
	}
	signer, err := client.LoadOrCreatePrivateKey(options.privateKeyPath, identity)
	if err != nil {
		return err
	}
	keyName := v1alpha1.KeyName(options.identityDefault)
	registration := v1alpha1.UserRegistration{
		TypeMeta: metav1.TypeMeta{APIVersion: v1alpha1.APIVersion, Kind: v1alpha1.UserRegistrationKind},
		ObjectMeta: metav1.ObjectMeta{
			Name:      v1alpha1.ResourceName(identity),
			Namespace: options.namespace,
		},
		Spec: v1alpha1.UserRegistrationSpec{
			Identity: identity,
			PublicKeys: []v1alpha1.SSHPublicKey{{
				Name: keyName,
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
	accepted, err := client.SubmitUserRegistration(ctx, options.httpClient, options.apiURL, registration,
		client.WithBearerToken(bearerToken))
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
	if authentication.Mode == authn.MethodOIDC {
		if _, err := client.ExchangeSSHCertificate(
			ctx, options.httpClient, options.apiURL, authentication, bearerToken, keyName, identity, signer,
		); err != nil {
			return fmt.Errorf("verify SSH certificate exchange: %w", err)
		}
	}
	saved := profile.Profile{
		ServerAddress:      options.serverAddress,
		APIURL:             options.apiURL,
		Namespace:          options.namespace,
		Identity:           identity,
		AuthenticationMode: authentication.Mode,
		KeyName:            keyName,
		OIDCDevice:         options.oidcDevice,
		PrivateKey:         options.privateKeyPath,
		KnownHosts:         options.knownHostsPath,
		Insecure:           options.insecure,
	}
	if err := profile.Save(options.profilePath, saved); err != nil {
		return fmt.Errorf("save login: %w", err)
	}
	slog.Info("login prepared", "identity", identity, "config", options.profilePath, "registration", options.registrationPath)
	_, err = fmt.Fprintln(output, options.registrationPath)
	return err
}

func loadRegistrationToken(path string) (string, error) {
	if strings.TrimSpace(path) == "" {
		return strings.TrimSpace(os.Getenv("TEARENV_REGISTRATION_TOKEN")), nil
	}
	info, err := os.Stat(path)
	if err != nil {
		return "", fmt.Errorf("stat registration token file %q: %w", path, err)
	}
	if info.Mode().Perm()&0o077 != 0 {
		return "", fmt.Errorf("registration token file %q permissions are %o; want 600 or stricter", path, info.Mode().Perm())
	}
	contents, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("read registration token file %q: %w", path, err)
	}
	token := strings.TrimSpace(string(contents))
	if token == "" {
		return "", fmt.Errorf("registration token file %q is empty", path)
	}
	return token, nil
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

func services(ctx context.Context, options servicesOptions, output, prompts io.Writer) error {
	saved, err := profile.Load(options.profilePath)
	if err != nil {
		return fmt.Errorf("load login; run 'tearenv login' first: %w", err)
	}
	hostKey, err := hostKeyCallback(saved.KnownHosts, saved.Insecure)
	if err != nil {
		return err
	}
	clientConfig, err := serviceClientConfig(ctx, saved, hostKey, options.httpClient, prompts, options.oidcDevice)
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

func connect(ctx context.Context, options connectOptions, specifications []string, prompts io.Writer) error {
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
	clientConfig, err := serviceClientConfig(ctx, saved, hostKey, options.httpClient, prompts, options.oidcDevice)
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

func serviceClientConfig(
	ctx context.Context,
	saved *profile.Profile,
	hostKey ssh.HostKeyCallback,
	httpClient *http.Client,
	prompts io.Writer,
	forceDevice bool,
) (client.ServiceClientConfig, error) {
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
		if saved.AuthenticationMode == authn.MethodOIDC {
			authentication, err := client.DiscoverAuthentication(ctx, httpClient, saved.APIURL)
			if err != nil {
				return client.ServiceClientConfig{}, err
			}
			if authentication.Mode != authn.MethodOIDC || authentication.OIDC == nil {
				return client.ServiceClientConfig{}, errors.New("server no longer advertises OIDC authentication")
			}
			oidcLogin, err := client.OIDCLogin(ctx, httpClient, *authentication.OIDC, client.OIDCLoginOptions{
				Device: saved.OIDCDevice || forceDevice, Output: prompts,
			})
			if err != nil {
				return client.ServiceClientConfig{}, err
			}
			if oidcLogin.Identity != saved.Identity {
				return client.ServiceClientConfig{}, fmt.Errorf("OIDC identity %q does not match saved identity %q", oidcLogin.Identity, saved.Identity)
			}
			signer, err = client.ExchangeSSHCertificate(
				ctx, httpClient, saved.APIURL, authentication, oidcLogin.SubjectToken, saved.KeyName, saved.Identity, signer,
			)
			if err != nil {
				return client.ServiceClientConfig{}, err
			}
		}
		config.Signer = signer
	}
	return config, nil
}
