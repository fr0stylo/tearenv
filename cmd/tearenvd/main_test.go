package main

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/fr0stylo/tearenv/internal/blueprint"
	"github.com/spf13/cobra"
)

func TestRootCommandExposesGatewayWorkflow(t *testing.T) {
	root := newRootCommand()

	tests := []struct {
		path  []string
		flags []string
	}{
		{path: []string{"serve"}, flags: []string{
			"listen", "metrics-listen", "host-key", "users", "authorized-keys", "blueprint", "scaler", "kubernetes",
		}},
		{path: []string{"invite"}, flags: []string{"users", "identity"}},
		{path: []string{"service", "grant"}, flags: []string{
			"users", "identity", "name", "target", "local-port", "workload-kind",
			"workload-namespace", "workload-name", "replicas", "ready-timeout", "idle-timeout",
		}},
		{path: []string{"blueprint", "init"}, flags: []string{"name"}},
	}

	for _, test := range tests {
		t.Run(strings.Join(test.path, " "), func(t *testing.T) {
			command := findCommand(t, root, test.path...)
			for _, name := range test.flags {
				if command.Flags().Lookup(name) == nil {
					t.Errorf("flag --%s is missing", name)
				}
			}
		})
	}
}

func TestBlueprintInitWritesValidStarterBlueprint(t *testing.T) {
	root := newRootCommand()
	var output bytes.Buffer
	root.SetOut(&output)
	root.SetErr(&output)
	root.SetArgs([]string{"blueprint", "init", "--name", "team-environment"})

	if err := root.Execute(); err != nil {
		t.Fatal(err)
	}
	document, err := blueprint.Load(output.Bytes())
	if err != nil {
		t.Fatalf("load generated blueprint: %v\n%s", err, output.String())
	}
	if document.Metadata.Name != "team-environment" {
		t.Fatalf("metadata.name = %q, want team-environment", document.Metadata.Name)
	}
}

func TestBlueprintInitRejectsInvalidName(t *testing.T) {
	root := newRootCommand()
	root.SetArgs([]string{"blueprint", "init", "--name", "Not Valid"})

	err := root.Execute()
	if err == nil || !strings.Contains(err.Error(), "metadata.name") {
		t.Fatalf("Execute() error = %v, want invalid metadata.name", err)
	}
}

func TestNewMetricsEndpointIncludesAutomaticCollectors(t *testing.T) {
	metrics, handler, err := newMetricsEndpoint()
	if err != nil {
		t.Fatal(err)
	}
	metrics.SetReady(true)

	request := httptest.NewRequest(http.MethodGet, "/metrics", nil)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("GET /metrics status = %d, want %d", response.Code, http.StatusOK)
	}
	for _, name := range []string{
		"go_goroutines",
		"process_cpu_seconds_total",
		"promhttp_metric_handler_requests_total",
		"tearenv_daemon_ready",
		"tearenv_engine_managed_workloads",
	} {
		if !strings.Contains(response.Body.String(), name) {
			t.Errorf("metrics output does not contain %q", name)
		}
	}
}

func TestRootCommandProvidesGeneratedHelp(t *testing.T) {
	root := newRootCommand()
	var output bytes.Buffer
	root.SetOut(&output)
	root.SetErr(&output)
	root.SetArgs([]string{"--help"})

	if err := root.Execute(); err != nil {
		t.Fatal(err)
	}
	for _, text := range []string{"Usage:", "Available Commands:", "blueprint", "invite", "serve", "service"} {
		if !strings.Contains(output.String(), text) {
			t.Errorf("help does not contain %q:\n%s", text, output.String())
		}
	}
}

func TestRootCommandWithoutSubcommandShowsHelp(t *testing.T) {
	root := newRootCommand()
	var output bytes.Buffer
	root.SetOut(&output)
	root.SetErr(&output)
	root.SetArgs(nil)

	if err := root.Execute(); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(output.String(), "tearenvd [command]") {
		t.Errorf("help does not contain root usage:\n%s", output.String())
	}
}

func TestInviteRequiresIdentityFlag(t *testing.T) {
	root := newRootCommand()
	var output bytes.Buffer
	root.SetOut(&output)
	root.SetErr(&output)
	root.SetArgs([]string{"invite"})

	err := root.Execute()
	if err == nil || !strings.Contains(err.Error(), `required flag(s) "identity" not set`) {
		t.Fatalf("Execute() error = %v", err)
	}
}

func findCommand(t *testing.T, root *cobra.Command, path ...string) *cobra.Command {
	t.Helper()
	command, remaining, err := root.Find(path)
	if err != nil {
		t.Fatalf("find command %q: %v", strings.Join(path, " "), err)
	}
	if len(remaining) != 0 {
		t.Fatalf("command %q was not found; remaining arguments: %v", strings.Join(path, " "), remaining)
	}
	return command
}

func TestNewScalerBackendAllowsStaticServices(t *testing.T) {
	backend, err := newScalerBackend("")
	if err != nil {
		t.Fatal(err)
	}
	if backend != nil {
		t.Fatalf("backend = %#v, want nil", backend)
	}
}

func TestNewScalerBackendRejectsUnknownBackend(t *testing.T) {
	_, err := newScalerBackend("docker")
	if err == nil || !strings.Contains(err.Error(), `unsupported scaler backend "docker"`) {
		t.Fatalf("newScalerBackend() error = %v", err)
	}
}
