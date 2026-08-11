package main

import (
	"bytes"
	"strings"
	"testing"

	"github.com/spf13/cobra"
)

func TestRootCommandExposesGatewayWorkflow(t *testing.T) {
	root := newRootCommand()

	tests := []struct {
		path  []string
		flags []string
	}{
		{path: []string{"serve"}, flags: []string{"listen", "host-key", "users", "authorized-keys", "scaler", "kubernetes"}},
		{path: []string{"invite"}, flags: []string{"users", "identity"}},
		{path: []string{"service", "grant"}, flags: []string{
			"users", "identity", "name", "target", "local-port", "workload-kind",
			"workload-namespace", "workload-name", "replicas", "ready-timeout", "idle-timeout",
		}},
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

func TestRootCommandProvidesGeneratedHelp(t *testing.T) {
	root := newRootCommand()
	var output bytes.Buffer
	root.SetOut(&output)
	root.SetErr(&output)
	root.SetArgs([]string{"--help"})

	if err := root.Execute(); err != nil {
		t.Fatal(err)
	}
	for _, text := range []string{"Usage:", "Available Commands:", "invite", "serve", "service"} {
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
