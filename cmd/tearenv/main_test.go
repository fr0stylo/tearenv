package main

import (
	"bytes"
	"strings"
	"testing"

	"github.com/spf13/cobra"
)

func TestRootCommandExposesDeveloperWorkflow(t *testing.T) {
	root := newRootCommand()

	tests := []struct {
		path  []string
		flags []string
	}{
		{path: []string{"login"}, flags: []string{
			"method", "server", "identity", "invite", "private-key", "config", "known-hosts",
			"kubeconfig", "kubernetes-context", "kubernetes-namespace", "kubernetes-secret",
			"insecure-skip-host-key-check",
		}},
		{path: []string{"services"}, flags: []string{"config"}},
		{path: []string{"connect"}, flags: []string{"config", "listen-host", "server", "identity", "token", "private-key", "known-hosts", "insecure-skip-host-key-check"}},
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
	for _, text := range []string{"Usage:", "Available Commands:", "connect", "login", "services"} {
		if !strings.Contains(output.String(), text) {
			t.Errorf("help does not contain %q:\n%s", text, output.String())
		}
	}
}

func TestRootCommandLeavesErrorRenderingToMain(t *testing.T) {
	root := newRootCommand()

	if !root.SilenceErrors {
		t.Error("SilenceErrors = false, want true")
	}
	if !root.SilenceUsage {
		t.Error("SilenceUsage = false, want true")
	}
}

func TestConnectAcceptsFlagsAfterServiceArguments(t *testing.T) {
	command := findCommand(t, newRootCommand(), "connect")
	if err := command.ParseFlags([]string{"postgres", "--listen-host", "127.0.0.2", "redis"}); err != nil {
		t.Fatal(err)
	}

	listenHost, err := command.Flags().GetString("listen-host")
	if err != nil {
		t.Fatal(err)
	}
	if listenHost != "127.0.0.2" {
		t.Errorf("listen host = %q, want 127.0.0.2", listenHost)
	}
	if got := command.Flags().Args(); strings.Join(got, ",") != "postgres,redis" {
		t.Errorf("service arguments = %v, want [postgres redis]", got)
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
