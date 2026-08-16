package main

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	v1alpha1 "github.com/fr0stylo/tearenv/api/v1alpha1"
	"github.com/fr0stylo/tearenv/internal/client"
	"github.com/fr0stylo/tearenv/internal/profile"
	"github.com/spf13/cobra"
	"golang.org/x/crypto/ssh"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestRootCommandExposesDeveloperWorkflow(t *testing.T) {
	root := newRootCommand()

	tests := []struct {
		path  []string
		flags []string
	}{
		{path: []string{"login"}, flags: []string{
			"api-url", "namespace", "server", "identity", "registration-token-file", "private-key", "registration", "config", "known-hosts", "insecure-skip-host-key-check",
		}},
		{path: []string{"services"}, flags: []string{"config"}},
		{path: []string{"connect"}, flags: []string{"config", "listen-host", "server", "identity", "private-key", "known-hosts", "insecure-skip-host-key-check"}},
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

func TestLoginCreatesLocalUserRegistration(t *testing.T) {
	t.Parallel()

	directory := t.TempDir()
	httpClient := acceptedRegistrationClient(t)
	options := loginOptions{
		serverAddress:    "gateway.example.com:2222",
		apiURL:           "https://api.example.com",
		namespace:        "default",
		identityDefault:  "workstation",
		privateKeyPath:   filepath.Join(directory, "id_ed25519"),
		registrationPath: filepath.Join(directory, "user-registration.yaml"),
		profilePath:      filepath.Join(directory, "config.json"),
		knownHostsPath:   filepath.Join(directory, "known_hosts"),
		httpClient:       httpClient,
	}
	var output bytes.Buffer
	var prompts bytes.Buffer
	if err := login(context.Background(), options, strings.NewReader("alice\n"), &output, &prompts); err != nil {
		t.Fatal(err)
	}

	if !strings.Contains(prompts.String(), "Identity [workstation]") {
		t.Fatalf("prompt = %q, want hostname default", prompts.String())
	}
	if strings.TrimSpace(output.String()) != options.registrationPath {
		t.Fatalf("output = %q, want registration path %q", output.String(), options.registrationPath)
	}

	contents, err := os.ReadFile(options.registrationPath)
	if err != nil {
		t.Fatal(err)
	}
	registration, err := v1alpha1.LoadUserRegistration(contents)
	if err != nil {
		t.Fatal(err)
	}
	if registration.Name != "alice" || registration.Spec.Identity != "alice" {
		t.Fatalf("registration identity = %q/%q, want alice", registration.Name, registration.Spec.Identity)
	}
	if len(registration.Spec.PublicKeys) != 1 || registration.Spec.PublicKeys[0].Name != "workstation" {
		t.Fatalf("registration public keys = %#v, want workstation", registration.Spec.PublicKeys)
	}

	signer, err := client.LoadPrivateKey(options.privateKeyPath)
	if err != nil {
		t.Fatal(err)
	}
	registeredKey, _, _, _, err := ssh.ParseAuthorizedKey([]byte(registration.Spec.PublicKeys[0].Key))
	if err != nil {
		t.Fatal(err)
	}
	if ssh.FingerprintSHA256(registeredKey) != ssh.FingerprintSHA256(signer.PublicKey()) {
		t.Fatal("registration public key does not match the saved private key")
	}

	saved, err := profile.Load(options.profilePath)
	if err != nil {
		t.Fatal(err)
	}
	if saved.Identity != "alice" || saved.PrivateKey != options.privateKeyPath || saved.Token != "" {
		t.Fatalf("saved profile = %#v, want public-key-only alice profile", saved)
	}
	for _, path := range []string{options.privateKeyPath, options.registrationPath, options.profilePath} {
		info, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		if got := info.Mode().Perm(); got != 0o600 {
			t.Errorf("%s permissions = %o, want 600", path, got)
		}
	}
}

func TestLoadRegistrationTokenRejectsOpenPermissions(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "registration-token")
	if err := os.WriteFile(path, make([]byte, 32), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := loadRegistrationToken(path); err == nil || !strings.Contains(err.Error(), "permissions") {
		t.Fatalf("loadRegistrationToken() error = %v, want permissions error", err)
	}
}

func TestLoginUsesHostnameDefaultAndExplicitIdentitySkipsPrompt(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		identity string
		input    string
		want     string
		prompt   bool
	}{
		{name: "empty prompt", input: "\n", want: "build-host", prompt: true},
		{name: "end of input", want: "build-host", prompt: true},
		{name: "identity flag", identity: "alice", want: "alice"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			directory := t.TempDir()
			httpClient := acceptedRegistrationClient(t)
			options := loginOptions{
				serverAddress:    "gateway.example.com:2222",
				apiURL:           "https://api.example.com",
				namespace:        "default",
				identity:         test.identity,
				identityDefault:  "build-host",
				privateKeyPath:   filepath.Join(directory, "id_ed25519"),
				registrationPath: filepath.Join(directory, "user-registration.yaml"),
				profilePath:      filepath.Join(directory, "config.json"),
				knownHostsPath:   filepath.Join(directory, "known_hosts"),
				httpClient:       httpClient,
			}
			var prompts bytes.Buffer
			if err := login(context.Background(), options, strings.NewReader(test.input), &bytes.Buffer{}, &prompts); err != nil {
				t.Fatal(err)
			}
			contents, err := os.ReadFile(options.registrationPath)
			if err != nil {
				t.Fatal(err)
			}
			registration, err := v1alpha1.LoadUserRegistration(contents)
			if err != nil {
				t.Fatal(err)
			}
			if registration.Spec.Identity != test.want {
				t.Fatalf("identity = %q, want %q", registration.Spec.Identity, test.want)
			}
			if got := prompts.Len() != 0; got != test.prompt {
				t.Fatalf("prompt written = %t, want %t; prompt: %q", got, test.prompt, prompts.String())
			}
		})
	}
}

func TestLoginWaitsForAPIAcceptanceBeforeSavingProfile(t *testing.T) {
	t.Parallel()

	directory := t.TempDir()
	httpClient := &http.Client{Transport: commandRoundTripFunc(func(request *http.Request) (*http.Response, error) {
		var registration v1alpha1.UserRegistration
		if err := json.NewDecoder(request.Body).Decode(&registration); err != nil {
			t.Fatalf("decode registration request: %v", err)
		}
		registration.Status = &v1alpha1.UserRegistrationStatus{Conditions: []metav1.Condition{{
			Type:               v1alpha1.ConditionAccepted,
			Status:             metav1.ConditionUnknown,
			Reason:             "PendingApproval",
			LastTransitionTime: metav1.Now(),
		}}}
		contents, err := json.Marshal(registration)
		if err != nil {
			t.Fatal(err)
		}
		return &http.Response{
			StatusCode: http.StatusAccepted,
			Status:     "202 Accepted",
			Header:     http.Header{"Content-Type": []string{"application/json"}},
			Body:       io.NopCloser(bytes.NewReader(contents)),
		}, nil
	})}
	options := loginOptions{
		serverAddress:    "gateway.example.com:2222",
		apiURL:           "https://api.example.com",
		namespace:        "default",
		identity:         "alice",
		identityDefault:  "workstation",
		privateKeyPath:   filepath.Join(directory, "id_ed25519"),
		registrationPath: filepath.Join(directory, "user-registration.yaml"),
		profilePath:      filepath.Join(directory, "config.json"),
		knownHostsPath:   filepath.Join(directory, "known_hosts"),
		httpClient:       httpClient,
	}
	err := login(context.Background(), options, strings.NewReader(""), &bytes.Buffer{}, &bytes.Buffer{})
	if err == nil || !strings.Contains(err.Error(), "pending approval") {
		t.Fatalf("login() error = %v, want pending approval", err)
	}
	if _, err := os.Stat(options.profilePath); !os.IsNotExist(err) {
		t.Fatalf("profile stat error = %v, want not exists", err)
	}
	contents, err := os.ReadFile(options.registrationPath)
	if err != nil {
		t.Fatal(err)
	}
	registration, err := v1alpha1.LoadUserRegistration(contents)
	if err != nil {
		t.Fatal(err)
	}
	if registration.Status == nil || registration.Status.Conditions[0].Reason != "PendingApproval" {
		t.Fatalf("saved registration status = %#v, want pending approval", registration.Status)
	}
}

func acceptedRegistrationClient(t *testing.T) *http.Client {
	t.Helper()
	return &http.Client{Transport: commandRoundTripFunc(func(request *http.Request) (*http.Response, error) {
		var registration v1alpha1.UserRegistration
		if err := json.NewDecoder(request.Body).Decode(&registration); err != nil {
			t.Fatalf("decode registration request: %v", err)
		}
		registration.Status = &v1alpha1.UserRegistrationStatus{
			ObservedGeneration: 1,
			Conditions: []metav1.Condition{{
				Type:               v1alpha1.ConditionAccepted,
				Status:             metav1.ConditionTrue,
				Reason:             "Approved",
				LastTransitionTime: metav1.Now(),
			}},
		}
		contents, err := json.Marshal(registration)
		if err != nil {
			t.Fatal(err)
		}
		return &http.Response{
			StatusCode: http.StatusCreated,
			Status:     "201 Created",
			Header:     http.Header{"Content-Type": []string{"application/json"}},
			Body:       io.NopCloser(bytes.NewReader(contents)),
		}, nil
	})}
}

type commandRoundTripFunc func(*http.Request) (*http.Response, error)

func (function commandRoundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return function(request)
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
