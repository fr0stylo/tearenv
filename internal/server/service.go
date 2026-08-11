package server

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net"
	"strconv"

	"github.com/fr0stylo/tearenv/internal/authorization"
	"github.com/fr0stylo/tearenv/internal/protocol"
	"github.com/fr0stylo/tearenv/internal/proxy"
	gliderssh "github.com/gliderlabs/ssh"
	"golang.org/x/crypto/ssh"
)

type directTCPIPRequest struct {
	ServiceName string
	ServicePort uint32
	OriginHost  string
	OriginPort  uint32
}

var ErrServiceDenied = errors.New("service access denied")

// ServiceGateway is the runtime boundary for service discovery and connection.
// A backend implementation can scale a workload while this boundary waits for
// readiness, dials it, and tracks idleness without changing the SSH protocol.
type ServiceGateway interface {
	Services(ctx context.Context, identity string) ([]authorization.Service, error)
	Open(ctx context.Context, identity, name string) (net.Conn, authorization.Service, error)
}

func serviceCatalogHandler(gateway ServiceGateway, logger *slog.Logger) gliderssh.RequestHandler {
	return func(ctx gliderssh.Context, _ *gliderssh.Server, _ *ssh.Request) (bool, []byte) {
		if _, enrollment := protocol.EnrollmentIdentity(ctx.User()); enrollment {
			return false, nil
		}
		services, err := gateway.Services(ctx, ctx.User())
		if err != nil {
			logger.Warn("service catalog failed", "identity", ctx.User(), "error", err)
			return false, nil
		}
		response := make([]protocol.Service, 0, len(services))
		for _, service := range services {
			response = append(response, protocol.Service{Name: service.Name, LocalPort: service.LocalPort})
		}
		payload, err := json.Marshal(response)
		if err != nil {
			return false, nil
		}
		return true, payload
	}
}

func serviceChannelHandler(gateway ServiceGateway, logger *slog.Logger) gliderssh.ChannelHandler {
	return func(_ *gliderssh.Server, _ *ssh.ServerConn, newChannel ssh.NewChannel, ctx gliderssh.Context) {
		if _, enrollment := protocol.EnrollmentIdentity(ctx.User()); enrollment {
			_ = newChannel.Reject(ssh.Prohibited, "enrollment connections cannot access services")
			return
		}
		var request directTCPIPRequest
		if err := ssh.Unmarshal(newChannel.ExtraData(), &request); err != nil || request.ServicePort != 0 {
			_ = newChannel.Reject(ssh.ConnectionFailed, "invalid service request")
			return
		}
		target, service, err := gateway.Open(ctx, ctx.User(), request.ServiceName)
		if errors.Is(err, ErrServiceDenied) {
			logger.Warn("service access denied", "identity", ctx.User(), "service", request.ServiceName)
			_ = newChannel.Reject(ssh.Prohibited, "service is not granted to this identity")
			return
		}
		if err != nil {
			logger.Warn("service target failed", "identity", ctx.User(), "service", service.Name, "error", err)
			_ = newChannel.Reject(ssh.ConnectionFailed, "service target is unavailable")
			return
		}
		channel, requests, err := newChannel.Accept()
		if err != nil {
			_ = target.Close()
			return
		}
		go ssh.DiscardRequests(requests)
		logger.Info("service connection opened",
			"identity", ctx.User(),
			"service", service.Name,
			"target", service.Target,
			"origin", net.JoinHostPort(request.OriginHost, strconv.Itoa(int(request.OriginPort))),
		)
		proxyWithContext(ctx, channel, target)
	}
}

func proxyWithContext(ctx context.Context, left ssh.Channel, right net.Conn) {
	done := make(chan struct{})
	defer close(done)
	go func() {
		select {
		case <-ctx.Done():
			_ = left.Close()
			_ = right.Close()
		case <-done:
		}
	}()
	proxy.Join(left, right)
}
