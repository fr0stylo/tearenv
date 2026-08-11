package client_test

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"io"
	"log/slog"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/fr0stylo/tearenv/internal/authorization"
	"github.com/fr0stylo/tearenv/internal/client"
	"github.com/fr0stylo/tearenv/internal/server"
	"golang.org/x/crypto/ssh"
)

func TestIdentityAuthorizedService(t *testing.T) {
	target := startEchoServer(t)
	credentialsPath := filepath.Join(t.TempDir(), "users.json")
	invite, err := authorization.CreateInvite(credentialsPath, "alice")
	if err != nil {
		t.Fatal(err)
	}
	if err := authorization.GrantService(credentialsPath, "alice", authorization.Service{
		Name: "redis", Target: target, LocalPort: 6379,
	}); err != nil {
		t.Fatal(err)
	}
	credentials, err := authorization.LoadCredentials(credentialsPath)
	if err != nil {
		t.Fatal(err)
	}
	token, err := credentials.Enroll("alice", invite)
	if err != nil {
		t.Fatal(err)
	}

	signer := newSigner(t)
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	gateway, err := server.New(server.Config{
		Authenticator: credentials, Enrollment: credentials, Policy: credentials, Signer: signer, Logger: logger,
	})
	if err != nil {
		t.Fatal(err)
	}
	sshListener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	go func() { _ = gateway.Serve(ctx, sshListener) }()

	config := client.ServiceClientConfig{
		ServerAddress: sshListener.Addr().String(),
		Identity:      "alice",
		Token:         token,
		HostKey:       ssh.FixedHostKey(signer.PublicKey()),
		Logger:        logger,
	}
	catalog, err := client.ListServices(ctx, config)
	if err != nil {
		t.Fatal(err)
	}
	if len(catalog) != 1 || catalog[0].Name != "redis" || catalog[0].LocalPort != 6379 {
		t.Fatalf("catalog = %#v", catalog)
	}

	ready := make(chan net.Addr, 1)
	clientErrors := make(chan error, 1)
	go func() {
		clientErrors <- client.RunServices(ctx, config, []client.LocalService{{
			Name: "redis", ListenAddress: "127.0.0.1:0",
		}}, func(_ string, address net.Addr) { ready <- address })
	}()
	var local net.Addr
	select {
	case local = <-ready:
	case err := <-clientErrors:
		t.Fatalf("client stopped before service was ready: %v", err)
	case <-time.After(3 * time.Second):
		t.Fatal("timed out waiting for local service")
	}
	assertEcho(t, local.String(), "hello through redis service")
}

func TestPublicKeyIdentityListsAuthorizedServices(t *testing.T) {
	policyPath := filepath.Join(t.TempDir(), "access.json")
	if err := authorization.GrantService(policyPath, "alice", authorization.Service{
		Name: "redis", Target: "redis.dev.svc:6379", LocalPort: 6379,
	}); err != nil {
		t.Fatal(err)
	}
	policy, err := authorization.LoadCredentials(policyPath)
	if err != nil {
		t.Fatal(err)
	}
	clientSigner := newSigner(t)
	contents, err := authorization.UpsertPublicKey(nil, "alice", clientSigner.PublicKey())
	if err != nil {
		t.Fatal(err)
	}
	keysPath := filepath.Join(t.TempDir(), authorization.PublicKeysDataKey)
	if err := os.WriteFile(keysPath, contents, 0o600); err != nil {
		t.Fatal(err)
	}
	keys, err := authorization.LoadPublicKeys(keysPath)
	if err != nil {
		t.Fatal(err)
	}
	authenticator, err := authorization.NewChain(keys)
	if err != nil {
		t.Fatal(err)
	}
	hostSigner := newSigner(t)
	gateway, err := server.New(server.Config{
		Authenticator: authenticator,
		Policy:        policy,
		Signer:        hostSigner,
		Logger:        slog.New(slog.NewTextHandler(io.Discard, nil)),
	})
	if err != nil {
		t.Fatal(err)
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	go func() { _ = gateway.Serve(ctx, listener) }()

	catalog, err := client.ListServices(ctx, client.ServiceClientConfig{
		ServerAddress: listener.Addr().String(),
		Identity:      "alice",
		Signer:        clientSigner,
		HostKey:       ssh.FixedHostKey(hostSigner.PublicKey()),
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(catalog) != 1 || catalog[0].Name != "redis" {
		t.Fatalf("catalog = %#v, want redis", catalog)
	}
}

func newSigner(t *testing.T) ssh.Signer {
	t.Helper()
	_, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	signer, err := ssh.NewSignerFromKey(privateKey)
	if err != nil {
		t.Fatal(err)
	}
	return signer
}

func startEchoServer(t *testing.T) string {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = listener.Close() })
	go func() {
		for {
			connection, err := listener.Accept()
			if err != nil {
				return
			}
			go func() {
				defer connection.Close()
				_, _ = io.Copy(connection, connection)
			}()
		}
	}()
	return listener.Addr().String()
}

func assertEcho(t *testing.T, address, message string) {
	t.Helper()
	connection, err := net.DialTimeout("tcp", address, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer connection.Close()
	if _, err := io.WriteString(connection, message); err != nil {
		t.Fatal(err)
	}
	response := make([]byte, len(message))
	if _, err := io.ReadFull(connection, response); err != nil {
		t.Fatal(err)
	}
	if string(response) != message {
		t.Fatalf("response = %q, want %q", response, message)
	}
}
