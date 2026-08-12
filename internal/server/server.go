// Package server implements the tearenv identity-aware service gateway.
package server

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"time"

	"github.com/fr0stylo/tearenv/internal/authorization"
	"github.com/fr0stylo/tearenv/internal/protocol"
	gliderssh "github.com/gliderlabs/ssh"
	"golang.org/x/crypto/ssh"
)

type Config struct {
	Authenticator authorization.Authenticator
	Enrollment    authorization.Enrollment
	Policy        authorization.Policy
	Provisioner   EnvironmentProvisioner
	Signer        ssh.Signer
	Logger        *slog.Logger
	Gateway       ServiceGateway
	Metrics       *Metrics
}

// EnvironmentProvisioner reconciles resources for a verified identity before
// the SSH login is accepted.
type EnvironmentProvisioner interface {
	Provision(ctx context.Context, identity string) error
}

type Server struct {
	ssh     *gliderssh.Server
	gateway ServiceGateway
}

func New(config Config) (*Server, error) {
	if config.Authenticator == nil {
		return nil, errors.New("authenticator is required")
	}
	if config.Signer == nil {
		return nil, errors.New("SSH host key is required")
	}
	if config.Logger == nil {
		config.Logger = slog.Default()
	}
	if config.Gateway == nil {
		if config.Policy == nil {
			return nil, errors.New("authorization policy is required")
		}
		config.Gateway = NewLifecycleGateway(config.Policy, nil, config.Logger, WithMetrics(config.Metrics))
	}
	requestHandlers := map[string]gliderssh.RequestHandler{
		protocol.ServicesRequestType: serviceCatalogHandler(config.Gateway, config.Logger, config.Metrics),
	}
	if config.Enrollment != nil {
		requestHandlers[protocol.EnrollRequestType] = enrollmentHandler(config.Enrollment, config.Logger)
	}
	provisionEnvironment := func(ctx gliderssh.Context, identity string, method authorization.Method) bool {
		if config.Provisioner == nil {
			return true
		}
		started := time.Now()
		if err := config.Provisioner.Provision(ctx, identity); err != nil {
			config.Metrics.observeEnvironmentProvision(metricResultError, time.Since(started))
			config.Metrics.observeAuthentication(method, metricResultError)
			config.Logger.Warn("client environment provisioning failed",
				"identity", identity, "method", method, "remote", ctx.RemoteAddr(), "error", err)
			return false
		}
		config.Metrics.observeEnvironmentProvision(metricResultSuccess, time.Since(started))
		return true
	}
	authenticate := func(ctx gliderssh.Context, attempt authorization.Attempt) bool {
		result, authenticated, err := config.Authenticator.Authenticate(ctx, attempt)
		if err != nil {
			config.Metrics.observeAuthentication(attempt.Method, metricResultError)
			config.Logger.Warn("client authentication provider failed",
				"identity", attempt.Identity, "method", attempt.Method, "remote", ctx.RemoteAddr(), "error", err)
			return false
		}
		if !authenticated || result.Identity != attempt.Identity {
			config.Metrics.observeAuthentication(attempt.Method, metricResultRejected)
			config.Logger.Warn("client authentication rejected",
				"identity", attempt.Identity, "method", attempt.Method, "remote", ctx.RemoteAddr())
			return false
		}
		if !provisionEnvironment(ctx, result.Identity, attempt.Method) {
			return false
		}
		config.Metrics.observeAuthentication(attempt.Method, metricResultSuccess)
		config.Logger.Info("client authenticated",
			"identity", result.Identity,
			"provider", result.Provider,
			"method", attempt.Method,
			"remote", ctx.RemoteAddr(),
			"client_version", ctx.ClientVersion(),
		)
		return true
	}

	sshServer := &gliderssh.Server{
		HostSigners: []gliderssh.Signer{config.Signer},
		PasswordHandler: func(ctx gliderssh.Context, password string) bool {
			identity := ctx.User()
			if enrollmentIdentity, enrollment := protocol.EnrollmentIdentity(identity); enrollment {
				method := authorization.Method("enrollment")
				authenticated := config.Enrollment != nil && config.Enrollment.AuthenticateInvite(enrollmentIdentity, password)
				if !authenticated {
					config.Metrics.observeAuthentication(method, metricResultRejected)
					config.Logger.Warn("client enrollment rejected", "identity", enrollmentIdentity, "remote", ctx.RemoteAddr())
					return false
				}
				if !provisionEnvironment(ctx, enrollmentIdentity, method) {
					return false
				}
				config.Metrics.observeAuthentication(method, metricResultSuccess)
				ctx.SetValue(enrollmentContextKey{}, enrollmentAttempt{Identity: enrollmentIdentity, Invite: password})
				config.Logger.Info("client enrollment authenticated", "identity", enrollmentIdentity, "remote", ctx.RemoteAddr())
				return true
			}

			return authenticate(ctx, authorization.Attempt{
				Identity: identity, Method: authorization.MethodPassword, Password: password,
			})
		},
		PublicKeyHandler: func(ctx gliderssh.Context, key gliderssh.PublicKey) bool {
			return authenticate(ctx, authorization.Attempt{
				Identity: ctx.User(), Method: authorization.MethodPublicKey, PublicKey: key,
			})
		},
		ChannelHandlers: map[string]gliderssh.ChannelHandler{
			"direct-tcpip": serviceChannelHandler(config.Gateway, config.Logger),
		},
		RequestHandlers: requestHandlers,
		ConnectionFailedCallback: func(connection net.Conn, err error) {
			config.Metrics.observeHandshakeFailure()
			config.Logger.Warn("SSH handshake failed", "remote", connection.RemoteAddr(), "error", err)
		},
	}
	return &Server{ssh: sshServer, gateway: config.Gateway}, nil
}

// Serve accepts SSH connections until ctx is cancelled or the listener fails.
func (server *Server) Serve(ctx context.Context, listener net.Listener) error {
	done := make(chan struct{})
	defer close(done)
	go func() {
		select {
		case <-ctx.Done():
			_ = server.ssh.Close()
		case <-done:
		}
	}()

	err := server.ssh.Serve(listener)
	if shutdown, ok := server.gateway.(interface{ Shutdown(context.Context) error }); ok {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		if shutdownErr := shutdown.Shutdown(shutdownCtx); shutdownErr != nil {
			_ = server.ssh.Close()
			return fmt.Errorf("shutdown service gateway: %w", shutdownErr)
		}
	}
	if errors.Is(err, gliderssh.ErrServerClosed) && ctx.Err() != nil {
		return nil
	}
	if err != nil {
		return fmt.Errorf("serve SSH: %w", err)
	}
	return nil
}
