package client

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"time"

	"github.com/fr0stylo/tearenv/internal/protocol"
	"github.com/fr0stylo/tearenv/internal/proxy"
	"golang.org/x/crypto/ssh"
)

type ServiceClientConfig struct {
	ServerAddress string
	Identity      string
	Token         string
	Signer        ssh.Signer
	HostKey       ssh.HostKeyCallback
	DialTimeout   time.Duration
	Logger        *slog.Logger
}

type LocalService struct {
	Name          string
	ListenAddress string
}

// ListServices returns the named services granted to the authenticated identity.
func ListServices(ctx context.Context, config ServiceClientConfig) ([]protocol.Service, error) {
	config.setDefaults()
	if err := config.validate(); err != nil {
		return nil, err
	}
	connection, err := config.dial(ctx)
	if err != nil {
		return nil, err
	}
	defer connection.Close()
	ok, payload, err := connection.SendRequest(protocol.ServicesRequestType, true, nil)
	if err != nil {
		return nil, fmt.Errorf("request services: %w", err)
	}
	if !ok {
		return nil, errors.New("service catalog request was rejected")
	}
	var services []protocol.Service
	if err := json.Unmarshal(payload, &services); err != nil {
		return nil, fmt.Errorf("decode service catalog: %w", err)
	}
	return services, nil
}

// RunServices opens local listeners and proxies them to identity-authorized services.
func RunServices(ctx context.Context, config ServiceClientConfig, services []LocalService, onReady func(string, net.Addr)) error {
	config.setDefaults()
	if err := config.validate(); err != nil {
		return err
	}
	if len(services) == 0 {
		return errors.New("at least one service is required")
	}
	seen := make(map[string]struct{}, len(services))
	for _, service := range services {
		if service.Name == "" || service.ListenAddress == "" {
			return errors.New("service name and listen address are required")
		}
		if _, exists := seen[service.Name]; exists {
			return fmt.Errorf("service %q was requested more than once", service.Name)
		}
		seen[service.Name] = struct{}{}
	}

	connection, err := config.dial(ctx)
	if err != nil {
		return err
	}
	defer connection.Close()
	listeners := make([]net.Listener, 0, len(services))
	defer func() {
		for _, listener := range listeners {
			_ = listener.Close()
		}
	}()
	for _, service := range services {
		listener, err := net.Listen("tcp", service.ListenAddress)
		if err != nil {
			return fmt.Errorf("listen for service %q on %s: %w", service.Name, service.ListenAddress, err)
		}
		listeners = append(listeners, listener)
		config.Logger.Info("service ready", "service", service.Name, "local", listener.Addr())
		if onReady != nil {
			onReady(service.Name, listener.Addr())
		}
	}

	done := make(chan struct{})
	defer close(done)
	go func() {
		select {
		case <-ctx.Done():
			for _, listener := range listeners {
				_ = listener.Close()
			}
			_ = connection.Close()
		case <-done:
		}
	}()

	errCh := make(chan error, len(listeners))
	for index, listener := range listeners {
		service := services[index]
		go acceptService(ctx, connection, listener, service, config.Logger, errCh)
	}
	select {
	case <-ctx.Done():
		return nil
	case err := <-errCh:
		return err
	}
}

func acceptService(ctx context.Context, connection *ssh.Client, listener net.Listener, service LocalService, logger *slog.Logger, errCh chan<- error) {
	for {
		local, err := listener.Accept()
		if err != nil {
			if ctx.Err() == nil && !errors.Is(err, net.ErrClosed) {
				errCh <- fmt.Errorf("accept local connection for service %q: %w", service.Name, err)
			}
			return
		}
		go connectService(ctx, connection, local, service.Name, logger)
	}
}

func connectService(ctx context.Context, connection *ssh.Client, local net.Conn, serviceName string, logger *slog.Logger) {
	remote, err := connection.Dial("tcp", net.JoinHostPort(serviceName, "0"))
	if err != nil {
		logger.Warn("service connection failed", "service", serviceName, "error", err)
		_ = local.Close()
		return
	}
	done := make(chan struct{})
	defer close(done)
	go func() {
		select {
		case <-ctx.Done():
			_ = local.Close()
			_ = remote.Close()
		case <-done:
		}
	}()
	proxy.Join(local, remote)
}

func (config *ServiceClientConfig) setDefaults() {
	if config.ServerAddress == "" {
		config.ServerAddress = DefaultServerAddress
	}
	if config.Identity == "" {
		config.Identity = DefaultIdentity
	}
	if config.DialTimeout == 0 {
		config.DialTimeout = DefaultDialTimeout
	}
	if config.Logger == nil {
		config.Logger = slog.Default()
	}
}

func (config ServiceClientConfig) validate() error {
	if config.Token == "" && config.Signer == nil {
		return errors.New("token or private key is required")
	}
	if config.HostKey == nil {
		return errors.New("host key verifier is required")
	}
	if config.DialTimeout < 0 {
		return errors.New("dial timeout cannot be negative")
	}
	return nil
}

func (config ServiceClientConfig) dial(ctx context.Context) (*ssh.Client, error) {
	authentication := make([]ssh.AuthMethod, 0, 2)
	if config.Signer != nil {
		authentication = append(authentication, ssh.PublicKeys(config.Signer))
	}
	if config.Token != "" {
		authentication = append(authentication, ssh.Password(config.Token))
	}
	sshConfig := &ssh.ClientConfig{
		User:            config.Identity,
		Auth:            authentication,
		HostKeyCallback: config.HostKey,
		Timeout:         config.DialTimeout,
	}
	connection, err := dialSSH(ctx, config.ServerAddress, sshConfig, config.DialTimeout)
	if err != nil {
		return nil, fmt.Errorf("connect to SSH server: %w", err)
	}
	return connection, nil
}
