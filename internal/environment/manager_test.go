package environment

import (
	"context"
	"errors"
	"slices"
	"testing"

	"github.com/fr0stylo/tearenv/internal/authorization"
	"github.com/fr0stylo/tearenv/internal/blueprint"
)

func TestManagerProvisionsAnEnvironmentAndPublishesItsServices(t *testing.T) {
	applier := &recordingApplier{}
	manager, err := NewManager(blueprint.Default("web-development"), applier, staticPolicy{
		{Name: "shared", Target: "shared.internal:443", LocalPort: 8443},
	})
	if err != nil {
		t.Fatal(err)
	}

	if err := manager.Provision(t.Context(), "alice"); err != nil {
		t.Fatal(err)
	}
	if len(applier.instances) != 1 || applier.instances[0].Namespace != "tearenv-alice-web-development" {
		t.Fatalf("applied instances = %#v", applier.instances)
	}

	services, err := manager.Services("alice")
	if err != nil {
		t.Fatal(err)
	}
	if names := serviceNames(services); !slices.Equal(names, []string{"shared", "web"}) {
		t.Fatalf("services = %v, want shared and web", names)
	}
	service, exists := manager.ResolveService("alice", "web")
	if !exists {
		t.Fatal("web service was not published")
	}
	if service.Target != "workspace.tearenv-alice-web-development.svc.cluster.local:80" {
		t.Fatalf("target = %q", service.Target)
	}
	if service.Workload == nil || service.Workload.Namespace != "tearenv-alice-web-development" || service.Workload.Name != "workspace" {
		t.Fatalf("workload = %#v", service.Workload)
	}
}

func TestManagerKeepsDeveloperNamespacesSeparate(t *testing.T) {
	applier := &recordingApplier{}
	manager, err := NewManager(blueprint.Default("developer-environment"), applier, staticPolicy(nil))
	if err != nil {
		t.Fatal(err)
	}

	for _, identity := range []string{"alice", "bob"} {
		if err := manager.Provision(t.Context(), identity); err != nil {
			t.Fatal(err)
		}
	}
	if len(applier.instances) != 2 || applier.instances[0].Namespace == applier.instances[1].Namespace {
		t.Fatalf("applied namespaces = %q and %q", applier.instances[0].Namespace, applier.instances[1].Namespace)
	}
}

func TestManagerDoesNotPublishServicesWhenReconciliationFails(t *testing.T) {
	want := errors.New("API unavailable")
	manager, err := NewManager(blueprint.Default("developer-environment"), failingApplier{err: want}, staticPolicy(nil))
	if err != nil {
		t.Fatal(err)
	}

	if err := manager.Provision(t.Context(), "alice"); !errors.Is(err, want) {
		t.Fatalf("Provision() error = %v, want %v", err, want)
	}
	if _, exists := manager.ResolveService("alice", "web"); exists {
		t.Fatal("failed environment published its service")
	}
}

type recordingApplier struct {
	instances []blueprint.Instance
}

func (applier *recordingApplier) Reconcile(_ context.Context, instance blueprint.Instance) error {
	applier.instances = append(applier.instances, instance)
	return nil
}

type failingApplier struct{ err error }

func (applier failingApplier) Reconcile(context.Context, blueprint.Instance) error {
	return applier.err
}

type staticPolicy []authorization.Service

func (policy staticPolicy) Services(string) ([]authorization.Service, error) {
	return append([]authorization.Service(nil), policy...), nil
}

func (policy staticPolicy) ResolveService(_ string, name string) (authorization.Service, bool) {
	for _, service := range policy {
		if service.Name == name {
			return service, true
		}
	}
	return authorization.Service{}, false
}

func serviceNames(services []authorization.Service) []string {
	names := make([]string, 0, len(services))
	for _, service := range services {
		names = append(names, service.Name)
	}
	return names
}
