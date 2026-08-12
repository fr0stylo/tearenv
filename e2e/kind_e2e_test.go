package e2e_test

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	v1alpha1 "github.com/fr0stylo/tearenv/api/v1alpha1"
	"github.com/fr0stylo/tearenv/internal/blueprint"
	"github.com/fr0stylo/tearenv/internal/client"
	"github.com/fr0stylo/tearenv/internal/profile"
	"github.com/fr0stylo/tearenv/internal/registration"
	"github.com/fr0stylo/tearenv/internal/server"
	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/knownhosts"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

const (
	kindE2EEnabled        = "TEARENV_KIND_E2E"
	kindE2EKeepCluster    = "TEARENV_KEEP_KIND_CLUSTER"
	kindNamespace         = "tearenv-e2e"
	kindAliceResponse     = "alice workload\n"
	kindBobResponse       = "bob workload\n"
	kindBlueprintResponse = "blueprint workload\n"
	kindCommandTimeout    = 5 * time.Minute
	kindConditionTimeout  = 90 * time.Second
)

func TestKubernetesScalingWithKind(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping Kind E2E test in short mode")
	}
	if os.Getenv(kindE2EEnabled) != "1" {
		t.Skip("set TEARENV_KIND_E2E=1 or run 'make e2e-kind' to run the Kind E2E test")
	}

	root := repositoryRoot(t)
	requireCommands(t, "docker", "kind", "kubectl", "go")
	mustRunCommand(t, root, nil, "docker", "info")

	temporary := t.TempDir()
	clusterName := "tearenv-e2e-" + strconv.FormatInt(time.Now().UnixNano(), 36)
	contextName := "kind-" + clusterName
	imageName := "tearenv-kind-e2e:" + strconv.FormatInt(time.Now().UnixNano(), 36)

	t.Cleanup(func() {
		if os.Getenv(kindE2EKeepCluster) == "1" {
			t.Logf("retaining Kind cluster %q with context %q", clusterName, contextName)
			return
		}
		output, err := runCommandWithTimeout(root, nil, kindCommandTimeout, "kind", "delete", "cluster", "--name", clusterName)
		if err != nil {
			t.Errorf("delete Kind cluster: %v\n%s", err, output)
		}
	})
	mustRunCommand(t, root, nil, "kind", "create", "cluster", "--name", clusterName)
	t.Cleanup(func() {
		if !t.Failed() {
			return
		}
		for _, arguments := range [][]string{
			{"--context", contextName, "-n", kindNamespace, "get", "all", "-o", "wide"},
			{"--context", contextName, "-n", kindNamespace, "get", "events", "--sort-by=.lastTimestamp"},
			{"--context", contextName, "-n", kindNamespace, "describe", "deployment", "tearenvd"},
			{"--context", contextName, "-n", kindNamespace, "describe", "deployment", "http-alice"},
			{"--context", contextName, "-n", kindNamespace, "describe", "deployment", "http-bob"},
			{"--context", contextName, "-n", kindNamespace, "logs", "deployment/tearenvd", "--tail=-1"},
		} {
			output, err := runCommandWithTimeout(root, nil, 30*time.Second, "kubectl", arguments...)
			t.Logf("kubectl %s\nerror: %v\n%s", strings.Join(arguments, " "), err, output)
		}
		logs, _ := filepath.Glob(filepath.Join(temporary, "*.log"))
		for _, path := range logs {
			contents, err := os.ReadFile(path)
			t.Logf("%s\nerror: %v\n%s", filepath.Base(path), err, contents)
		}
	})

	clientBinary := filepath.Join(temporary, "tearenv")
	buildBinary(t, root, clientBinary, "./cmd/tearenv")
	imageContext := filepath.Join(temporary, "image")
	if err := os.Mkdir(imageContext, 0o700); err != nil {
		t.Fatal(err)
	}
	buildLinuxBinary(t, root, filepath.Join(imageContext, "tearenvd"), "./cmd/tearenvd")
	buildLinuxBinary(t, root, filepath.Join(imageContext, "http-server"), "./e2e/kind/httpserver")
	mustRunCommand(t, root, nil, "docker", "build",
		"-f", filepath.Join(root, "e2e", "kind", "Dockerfile"),
		"-t", imageName,
		imageContext,
	)
	t.Cleanup(func() {
		output, err := runCommandWithTimeout(root, nil, 30*time.Second, "docker", "image", "rm", imageName)
		if err != nil {
			t.Logf("remove test image %q: %v\n%s", imageName, err, output)
		}
	})
	mustRunCommand(t, root, nil, "kind", "load", "docker-image", imageName, "--name", clusterName)

	hostKeyPath := filepath.Join(temporary, "host_key")
	signer, err := server.LoadOrCreateHostKey(hostKeyPath)
	if err != nil {
		t.Fatal(err)
	}
	credentialsPath := filepath.Join(temporary, "users.json")
	writeKindCredentials(t, credentialsPath)
	registrationsPath := filepath.Join(temporary, "registrations")
	privateKeys := writeKindRegistrations(t, registrationsPath, temporary)
	blueprintPath := filepath.Join(temporary, "blueprint.yaml")
	writeKindBlueprint(t, blueprintPath, imageName)

	mustRunCommand(t, root, nil, "kubectl", "--context", contextName, "create", "namespace", kindNamespace)
	mustRunCommand(t, root, nil, "kubectl", "--context", contextName, "-n", kindNamespace,
		"create", "secret", "generic", "tearenvd-config",
		"--from-file=users.json="+credentialsPath,
		"--from-file=host_key="+hostKeyPath,
		"--from-file=blueprint.yaml="+blueprintPath,
		"--from-file=alice.yaml="+filepath.Join(registrationsPath, "default", "alice.yaml"),
		"--from-file=bob.yaml="+filepath.Join(registrationsPath, "default", "bob.yaml"),
	)
	manifestPath := filepath.Join(root, "e2e", "kind", "manifests.yaml")
	manifest, err := os.ReadFile(manifestPath)
	if err != nil {
		t.Fatal(err)
	}
	manifestText := strings.ReplaceAll(string(manifest), "TEARENV_KIND_IMAGE", imageName)
	if manifestText == string(manifest) {
		t.Fatal("Kind manifest does not contain the image placeholder")
	}
	mustRunCommandWithInput(t, root, manifestText, "kubectl", "--context", contextName, "apply", "-f", "-")
	mustRunCommand(t, root, nil, "kubectl", "--context", contextName, "-n", kindNamespace,
		"rollout", "status", "deployment/tearenvd", "--timeout=90s",
	)

	for _, deployment := range []string{"http-alice", "http-bob"} {
		waitForDeploymentReplicas(t, root, contextName, deployment, "0", kindConditionTimeout)
	}

	sshAddress := reserveAddress(t)
	_, sshPort, err := net.SplitHostPort(sshAddress)
	if err != nil {
		t.Fatal(err)
	}
	portForward := startProcess(t, root, filepath.Join(temporary, "port-forward.log"),
		"kubectl", "--context", contextName, "-n", kindNamespace,
		"port-forward", "--address=127.0.0.1", "service/tearenvd", sshPort+":2222",
	)
	t.Cleanup(func() { portForward.stop(t) })
	waitForLog(t, portForward.logPath, "Forwarding from "+sshAddress)

	knownHostsPath := filepath.Join(temporary, "known_hosts")
	knownHost := knownhosts.Line([]string{sshAddress}, signer.PublicKey()) + "\n"
	if err := os.WriteFile(knownHostsPath, []byte(knownHost), 0o600); err != nil {
		t.Fatal(err)
	}
	aliceAddress := startKindClient(t, root, temporary, clientBinary, sshAddress, knownHostsPath, "alice", privateKeys["alice"], "http")
	bobAddress := startKindClient(t, root, temporary, clientBinary, sshAddress, knownHostsPath, "bob", privateKeys["bob"], "http")
	aliceURL := "http://" + aliceAddress + "/"
	bobURL := "http://" + bobAddress + "/"
	aliceEnvironment := "tearenv-alice-developer-environment"
	bobEnvironment := "tearenv-bob-developer-environment"
	for _, namespace := range []string{aliceEnvironment, bobEnvironment} {
		waitForDeploymentReplicasInNamespace(t, root, contextName, namespace, "workspace", "0", kindConditionTimeout)
	}

	aliceBlueprintAddress := startKindClient(t, root, temporary, clientBinary, sshAddress, knownHostsPath, "alice", privateKeys["alice"], "web")
	bobBlueprintAddress := startKindClient(t, root, temporary, clientBinary, sshAddress, knownHostsPath, "bob", privateKeys["bob"], "web")
	assertHTTPResponse(t, "http://"+aliceBlueprintAddress+"/", kindBlueprintResponse)
	waitForDeploymentReplicasInNamespace(t, root, contextName, aliceEnvironment, "workspace", "1", 2*time.Second)
	waitForDeploymentReplicasInNamespace(t, root, contextName, bobEnvironment, "workspace", "0", 2*time.Second)
	waitForDeploymentReplicasInNamespace(t, root, contextName, aliceEnvironment, "workspace", "0", kindConditionTimeout)
	assertHTTPResponse(t, "http://"+bobBlueprintAddress+"/", kindBlueprintResponse)
	waitForDeploymentReplicasInNamespace(t, root, contextName, bobEnvironment, "workspace", "1", 2*time.Second)
	waitForDeploymentReplicasInNamespace(t, root, contextName, bobEnvironment, "workspace", "0", kindConditionTimeout)

	// Alice's first cold request must only start Alice's identity-bound workload.
	assertHTTPResponse(t, aliceURL, kindAliceResponse)
	waitForDeploymentReplicas(t, root, contextName, "http-alice", "1", 2*time.Second)
	waitForDeploymentReplicas(t, root, contextName, "http-bob", "0", 2*time.Second)
	waitForDeploymentReplicas(t, root, contextName, "http-alice", "0", kindConditionTimeout)
	waitForNoWorkloadPods(t, root, contextName, "app=tearenv-kind-http-alice", kindConditionTimeout)

	// A second cold request proves that a workload can be started again after it
	// has already completed an idle scale-down cycle.
	assertHTTPResponse(t, aliceURL, kindAliceResponse)
	waitForDeploymentReplicas(t, root, contextName, "http-alice", "1", 2*time.Second)
	waitForDeploymentReplicas(t, root, contextName, "http-alice", "0", kindConditionTimeout)
	waitForNoWorkloadPods(t, root, contextName, "app=tearenv-kind-http-alice", kindConditionTimeout)

	// Concurrent requests from both identities must all succeed and independently
	// start the workload assigned to each identity.
	assertHTTPBurst(t, []httpExpectation{
		{address: aliceURL, body: kindAliceResponse},
		{address: bobURL, body: kindBobResponse},
	}, 8)
	for _, deployment := range []string{"http-alice", "http-bob"} {
		waitForDeploymentReplicas(t, root, contextName, deployment, "1", 2*time.Second)
	}
	for _, workload := range []struct {
		deployment string
		selector   string
	}{
		{deployment: "http-alice", selector: "app=tearenv-kind-http-alice"},
		{deployment: "http-bob", selector: "app=tearenv-kind-http-bob"},
	} {
		waitForDeploymentReplicas(t, root, contextName, workload.deployment, "0", kindConditionTimeout)
		waitForNoWorkloadPods(t, root, contextName, workload.selector, kindConditionTimeout)
	}
}

func requireCommands(t *testing.T, commands ...string) {
	t.Helper()
	for _, command := range commands {
		if _, err := exec.LookPath(command); err != nil {
			t.Fatalf("required command %q is unavailable: %v", command, err)
		}
	}
}

func startKindClient(t *testing.T, root, temporary, clientBinary, sshAddress, knownHostsPath, identity, privateKey, service string) string {
	t.Helper()
	profilePath := filepath.Join(temporary, identity+"-"+service+"-profile.json")
	if err := profile.Save(profilePath, profile.Profile{
		ServerAddress: sshAddress,
		Identity:      identity,
		PrivateKey:    privateKey,
		KnownHosts:    knownHostsPath,
	}); err != nil {
		t.Fatal(err)
	}
	localAddress := reserveAddress(t)
	clientProcess := startProcess(t, root, filepath.Join(temporary, identity+"-"+service+"-client.log"),
		clientBinary, "connect", "--config", profilePath, service+"="+localAddress,
	)
	t.Cleanup(func() { clientProcess.stop(t) })
	waitForLog(t, clientProcess.logPath, "service ready")
	return localAddress
}

func writeKindBlueprint(t *testing.T, path, image string) {
	t.Helper()
	document := blueprint.Default("developer-environment")
	deployment := document.Spec.Resources[0]
	spec := deployment["spec"].(map[string]any)
	template := spec["template"].(map[string]any)
	podSpec := template["spec"].(map[string]any)
	containers := podSpec["containers"].([]any)
	container := containers[0].(map[string]any)
	container["image"] = image
	container["imagePullPolicy"] = "Never"
	container["command"] = []any{"/usr/local/bin/http-server"}
	container["env"] = []any{map[string]any{"name": "TEARENV_HTTP_RESPONSE", "value": "blueprint workload"}}
	container["ports"] = []any{map[string]any{"name": "http", "containerPort": 8080}}
	serviceSpec := document.Spec.Resources[1]["spec"].(map[string]any)
	serviceSpec["ports"] = []any{map[string]any{"name": "http", "port": 8080, "targetPort": "http"}}
	document.Spec.Services[0].LocalPort = 8080
	document.Spec.Services[0].Target.Port = 8080
	document.Spec.Services[0].Scale.ReadyTimeout = "45s"
	document.Spec.Services[0].Scale.IdleTimeout = "4s"

	contents, err := blueprint.Marshal(document)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, contents, 0o600); err != nil {
		t.Fatal(err)
	}
}

func buildLinuxBinary(t *testing.T, root, output, packagePath string) {
	t.Helper()
	environment := []string{"CGO_ENABLED=0", "GOOS=linux"}
	contents, err := runCommandWithTimeout(root, environment, kindCommandTimeout,
		"go", "build", "-o", output, packagePath,
	)
	if err != nil {
		t.Fatalf("build Linux binary %s: %v\n%s", packagePath, err, contents)
	}
}

func writeKindCredentials(t *testing.T, path string) {
	t.Helper()
	document := `{
  "access": {
    "alice": {
      "services": {
        "http": {
          "target": "http-alice.tearenv-e2e.svc.cluster.local:8080",
          "local_port": 8080,
          "workload": {
            "kind": "deployment",
            "namespace": "tearenv-e2e",
            "name": "http-alice",
            "replicas": 1,
            "ready_timeout": "45s",
            "idle_timeout": "4s"
          }
        }
      }
    },
    "bob": {
      "services": {
        "http": {
          "target": "http-bob.tearenv-e2e.svc.cluster.local:8080",
          "local_port": 8080,
          "workload": {
            "kind": "deployment",
            "namespace": "tearenv-e2e",
            "name": "http-bob",
            "replicas": 1,
            "ready_timeout": "45s",
            "idle_timeout": "4s"
          }
        }
      }
    }
  }
}
`
	if err := os.WriteFile(path, []byte(document), 0o600); err != nil {
		t.Fatal(err)
	}
}

func writeKindRegistrations(t *testing.T, registrationsPath, temporary string) map[string]string {
	t.Helper()
	store, err := registration.NewStore(registrationsPath, "default")
	if err != nil {
		t.Fatal(err)
	}
	privateKeys := make(map[string]string, 2)
	for _, identity := range []string{"alice", "bob"} {
		privateKey := filepath.Join(temporary, identity+"_id_ed25519")
		signer, err := client.LoadOrCreatePrivateKey(privateKey, identity)
		if err != nil {
			t.Fatal(err)
		}
		resource := v1alpha1.UserRegistration{
			TypeMeta: metav1.TypeMeta{APIVersion: v1alpha1.APIVersion, Kind: v1alpha1.UserRegistrationKind},
			ObjectMeta: metav1.ObjectMeta{
				Name:      v1alpha1.ResourceName(identity),
				Namespace: "default",
			},
			Spec: v1alpha1.UserRegistrationSpec{
				Identity: identity,
				PublicKeys: []v1alpha1.SSHPublicKey{{
					Name: "kind-e2e",
					Key:  strings.TrimSpace(string(ssh.MarshalAuthorizedKey(signer.PublicKey()))),
				}},
			},
		}
		if _, _, err := store.Put(resource); err != nil {
			t.Fatal(err)
		}
		privateKeys[identity] = privateKey
	}
	return privateKeys
}

func mustRunCommand(t *testing.T, workingDirectory string, environment []string, executable string, arguments ...string) string {
	t.Helper()
	output, err := runCommandWithTimeout(workingDirectory, environment, kindCommandTimeout, executable, arguments...)
	if err != nil {
		t.Fatalf("run %s %s: %v\n%s", executable, strings.Join(arguments, " "), err, output)
	}
	return output
}

func mustRunCommandWithInput(t *testing.T, workingDirectory, input, executable string, arguments ...string) string {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), kindCommandTimeout)
	defer cancel()
	command := exec.CommandContext(ctx, executable, arguments...)
	command.Dir = workingDirectory
	command.Env = os.Environ()
	command.Stdin = strings.NewReader(input)
	contents, err := command.CombinedOutput()
	if ctx.Err() != nil {
		t.Fatalf("run %s %s: %v\n%s", executable, strings.Join(arguments, " "), ctx.Err(), contents)
	}
	if err != nil {
		t.Fatalf("run %s %s: %v\n%s", executable, strings.Join(arguments, " "), err, contents)
	}
	return string(contents)
}

func runCommandWithTimeout(workingDirectory string, environment []string, timeout time.Duration, executable string, arguments ...string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	command := exec.CommandContext(ctx, executable, arguments...)
	command.Dir = workingDirectory
	command.Env = environmentWithOverrides(environment)
	contents, err := command.CombinedOutput()
	if ctx.Err() != nil {
		return string(contents), ctx.Err()
	}
	return string(contents), err
}

func environmentWithOverrides(overrides []string) []string {
	if len(overrides) == 0 {
		return os.Environ()
	}
	keys := make(map[string]struct{}, len(overrides))
	for _, override := range overrides {
		key, _, _ := strings.Cut(override, "=")
		keys[key] = struct{}{}
	}
	environment := make([]string, 0, len(os.Environ())+len(overrides))
	for _, variable := range os.Environ() {
		key, _, _ := strings.Cut(variable, "=")
		if _, overridden := keys[key]; !overridden {
			environment = append(environment, variable)
		}
	}
	return append(environment, overrides...)
}

func waitForDeploymentReplicas(t *testing.T, root, contextName, deployment, replicas string, timeout time.Duration) {
	t.Helper()
	waitForDeploymentReplicasInNamespace(t, root, contextName, kindNamespace, deployment, replicas, timeout)
}

func waitForDeploymentReplicasInNamespace(t *testing.T, root, contextName, namespace, deployment, replicas string, timeout time.Duration) {
	t.Helper()
	waitForKubernetesValue(t, root, contextName, timeout,
		[]string{"-n", namespace, "get", "deployment", deployment, "-o", "jsonpath={.spec.replicas}"},
		replicas,
	)
}

func waitForNoWorkloadPods(t *testing.T, root, contextName, selector string, timeout time.Duration) {
	t.Helper()
	waitForKubernetesValue(t, root, contextName, timeout,
		[]string{"-n", kindNamespace, "get", "pods", "-l", selector, "-o", "jsonpath={.items[*].metadata.name}"},
		"",
	)
}

func waitForKubernetesValue(t *testing.T, root, contextName string, timeout time.Duration, arguments []string, want string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	ticker := time.NewTicker(200 * time.Millisecond)
	defer ticker.Stop()
	var lastOutput string
	var lastError error
	for {
		commandArguments := append([]string{"--context", contextName}, arguments...)
		lastOutput, lastError = runCommandWithTimeout(root, nil, 10*time.Second, "kubectl", commandArguments...)
		if lastError == nil && strings.TrimSpace(lastOutput) == want {
			return
		}
		select {
		case <-ctx.Done():
			t.Fatalf("timed out waiting for kubectl %s = %q; last output %q, error %v",
				strings.Join(arguments, " "), want, strings.TrimSpace(lastOutput), lastError)
		case <-ticker.C:
		}
	}
}

func assertHTTPResponse(t *testing.T, address, want string) {
	t.Helper()
	if err := fetchHTTP(address, want); err != nil {
		t.Fatal(err)
	}
}

type httpExpectation struct {
	address string
	body    string
}

func assertHTTPBurst(t *testing.T, expectations []httpExpectation, requestsPerAddress int) {
	t.Helper()
	start := make(chan struct{})
	errors := make(chan error, len(expectations)*requestsPerAddress)
	var wait sync.WaitGroup
	for _, expectation := range expectations {
		for range requestsPerAddress {
			wait.Add(1)
			go func() {
				defer wait.Done()
				<-start
				if err := fetchHTTP(expectation.address, expectation.body); err != nil {
					errors <- err
				}
			}()
		}
	}
	close(start)
	wait.Wait()
	close(errors)
	for err := range errors {
		t.Error(err)
	}
	if t.Failed() {
		t.FailNow()
	}
}

func fetchHTTP(address, want string) error {
	transport := &http.Transport{DisableKeepAlives: true}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport, Timeout: kindConditionTimeout}
	request, err := http.NewRequestWithContext(context.Background(), http.MethodGet, address, nil)
	if err != nil {
		return fmt.Errorf("create GET %s: %w", address, err)
	}
	request.Close = true
	response, err := client.Do(request)
	if err != nil {
		return fmt.Errorf("GET %s: %w", address, err)
	}
	defer response.Body.Close()
	body, err := io.ReadAll(io.LimitReader(response.Body, 4096))
	if err != nil {
		return fmt.Errorf("read GET %s response: %w", address, err)
	}
	if response.StatusCode != http.StatusOK {
		return fmt.Errorf("GET %s status = %s, body = %q", address, response.Status, body)
	}
	if string(body) != want {
		return fmt.Errorf("GET %s body = %q, want %q", address, body, want)
	}
	return nil
}
