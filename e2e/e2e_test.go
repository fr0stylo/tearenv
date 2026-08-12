package e2e_test

import (
	"context"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/fr0stylo/tearenv/internal/server"
	"golang.org/x/crypto/ssh/knownhosts"
)

const testTimeout = 10 * time.Second

func TestBinariesAuthenticateIdentityAndForwardTraffic(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping binary E2E test in short mode")
	}

	root := repositoryRoot(t)
	temporary := t.TempDir()
	clientBinary := filepath.Join(temporary, "tearenv")
	serverBinary := filepath.Join(temporary, "tearenvd")
	buildBinary(t, root, clientBinary, "./cmd/tearenv")
	buildBinary(t, root, serverBinary, "./cmd/tearenvd")

	credentialsPath := filepath.Join(temporary, "users.json")
	target := startEchoServer(t)
	grantOutput, err := runCommand(root, nil, serverBinary,
		"service", "grant", "--users", credentialsPath,
		"--identity", "alice", "--name", "redis", "--target", target, "--local-port", "6379",
	)
	if err != nil {
		t.Fatalf("grant service: %v\n%s", err, grantOutput)
	}
	sshAddress := reserveAddress(t)
	apiAddress := reserveAddress(t)
	localAddress := reserveAddress(t)
	hostKeyPath := filepath.Join(temporary, "host_key")
	signer, err := server.LoadOrCreateHostKey(hostKeyPath)
	if err != nil {
		t.Fatal(err)
	}
	knownHostsPath := filepath.Join(temporary, "known_hosts")
	knownHost := knownhosts.Line([]string{sshAddress}, signer.PublicKey()) + "\n"
	if err := os.WriteFile(knownHostsPath, []byte(knownHost), 0o600); err != nil {
		t.Fatal(err)
	}

	serverProcess := startProcess(t, root, filepath.Join(temporary, "server.log"),
		serverBinary,
		"serve",
		"--listen", sshAddress,
		"--api-listen", apiAddress,
		"--host-key", hostKeyPath,
		"--users", credentialsPath,
		"--registrations", filepath.Join(temporary, "registrations"),
		"--metrics-listen", "",
	)
	t.Cleanup(func() { serverProcess.stop(t) })
	waitForLog(t, serverProcess.logPath, "tearenvd ready")

	profilePath := filepath.Join(temporary, "client-profile.json")
	privateKeyPath := filepath.Join(temporary, "id_ed25519")
	registrationPath := filepath.Join(temporary, "user-registration.yaml")
	loginOutput, err := runCommand(root, nil, clientBinary,
		"login",
		"--api-url", "http://"+apiAddress,
		"--identity", "alice",
		"--server", sshAddress,
		"--config", profilePath,
		"--private-key", privateKeyPath,
		"--registration", registrationPath,
		"--known-hosts", knownHostsPath,
	)
	if err != nil {
		t.Fatalf("login: %v\n%s", err, loginOutput)
	}
	clientProcess := startProcess(t, root, filepath.Join(temporary, "client.log"),
		clientBinary,
		"connect",
		"--config", profilePath,
		"redis="+localAddress,
	)
	t.Cleanup(func() { clientProcess.stop(t) })
	waitForLog(t, clientProcess.logPath, "service ready")

	assertEcho(t, localAddress, "crossing from local to cloud service")
	waitForLog(t, serverProcess.logPath, "identity=alice")
	waitForLog(t, serverProcess.logPath, "service connection opened")

	reusedOutput, reusedErr := runCommand(root, nil, clientBinary,
		"login",
		"--api-url", "http://"+apiAddress,
		"--identity", "alice",
		"--server", sshAddress,
		"--config", filepath.Join(temporary, "reused-profile.json"),
		"--private-key", privateKeyPath,
		"--registration", filepath.Join(temporary, "reused-registration.yaml"),
		"--known-hosts", knownHostsPath,
	)
	if reusedErr != nil {
		t.Fatalf("idempotent registration failed: %v\n%s", reusedErr, reusedOutput)
	}

	rejected := startProcess(t, root, filepath.Join(temporary, "rejected.log"),
		clientBinary,
		"connect",
		"--config", profilePath,
		"--identity", "bob",
		"redis="+reserveAddress(t),
	)
	select {
	case err := <-rejected.wait:
		if err == nil {
			t.Fatal("Bob authenticated with Alice's token")
		}
	case <-time.After(testTimeout):
		rejected.stop(t)
		t.Fatal("unauthorized client did not exit")
	}
	waitForLog(t, serverProcess.logPath, "client authentication rejected")
	waitForLog(t, serverProcess.logPath, "identity=bob")
}

type process struct {
	command *exec.Cmd
	logPath string
	wait    chan error
}

func startProcess(t *testing.T, workingDirectory, logPath, executable string, arguments ...string) *process {
	t.Helper()
	return startProcessWithEnvironment(t, workingDirectory, logPath, nil, executable, arguments...)
}

func startProcessWithEnvironment(t *testing.T, workingDirectory, logPath string, environment []string, executable string, arguments ...string) *process {
	t.Helper()
	logFile, err := os.Create(logPath)
	if err != nil {
		t.Fatal(err)
	}
	command := exec.Command(executable, arguments...)
	command.Dir = workingDirectory
	command.Env = append(os.Environ(), environment...)
	command.Stdout = logFile
	command.Stderr = logFile
	if err := command.Start(); err != nil {
		_ = logFile.Close()
		t.Fatalf("start %s: %v", filepath.Base(executable), err)
	}
	result := &process{command: command, logPath: logPath, wait: make(chan error, 1)}
	go func() {
		result.wait <- command.Wait()
		_ = logFile.Close()
	}()
	return result
}

func (process *process) stop(t *testing.T) {
	t.Helper()
	select {
	case <-process.wait:
		return
	default:
	}
	_ = process.command.Process.Signal(os.Interrupt)
	select {
	case <-process.wait:
	case <-time.After(testTimeout):
		_ = process.command.Process.Kill()
		t.Errorf("process %s did not stop", filepath.Base(process.command.Path))
	}
}

func repositoryRoot(t *testing.T) string {
	t.Helper()
	root, err := filepath.Abs("..")
	if err != nil {
		t.Fatal(err)
	}
	return root
}

func buildBinary(t *testing.T, root, output, packagePath string) {
	t.Helper()
	command := exec.Command("go", "build", "-o", output, packagePath)
	command.Dir = root
	if contents, err := command.CombinedOutput(); err != nil {
		t.Fatalf("build %s: %v\n%s", packagePath, err, contents)
	}
}

func runCommand(workingDirectory string, environment []string, executable string, arguments ...string) (string, error) {
	command := exec.Command(executable, arguments...)
	command.Dir = workingDirectory
	command.Env = append(os.Environ(), environment...)
	contents, err := command.CombinedOutput()
	return string(contents), err
}

func reserveAddress(t *testing.T) string {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	address := listener.Addr().String()
	if err := listener.Close(); err != nil {
		t.Fatal(err)
	}
	return address
}

func waitForLog(t *testing.T, path, text string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
	defer cancel()
	ticker := time.NewTicker(20 * time.Millisecond)
	defer ticker.Stop()
	for {
		contents, err := os.ReadFile(path)
		if err == nil && strings.Contains(string(contents), text) {
			return
		}
		select {
		case <-ctx.Done():
			t.Fatalf("timed out waiting for %q in %s\n%s", text, path, contents)
		case <-ticker.C:
		}
	}
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
	if _, err := fmt.Fprint(connection, message); err != nil {
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
