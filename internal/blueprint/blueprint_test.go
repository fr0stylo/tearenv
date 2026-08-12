package blueprint

import (
	"strings"
	"testing"

	"k8s.io/apimachinery/pkg/util/validation"
)

func TestDefaultCreatesIsolatedScalableEnvironment(t *testing.T) {
	document := Default("developer-environment")

	if document.APIVersion != APIVersion || document.Kind != Kind {
		t.Fatalf("type metadata = %s %s, want %s %s", document.APIVersion, document.Kind, APIVersion, Kind)
	}
	if document.Metadata.Name != "developer-environment" {
		t.Fatalf("metadata.name = %q, want developer-environment", document.Metadata.Name)
	}
	if !strings.Contains(document.Spec.Namespace.NameTemplate, IdentitySlugTemplate) {
		t.Fatalf("namespace template %q does not contain %q", document.Spec.Namespace.NameTemplate, IdentitySlugTemplate)
	}
	if !strings.Contains(document.Spec.Namespace.NameTemplate, BlueprintNameTemplate) {
		t.Fatalf("namespace template %q does not contain %q", document.Spec.Namespace.NameTemplate, BlueprintNameTemplate)
	}
	if len(document.Spec.Resources) != 2 {
		t.Fatalf("resources = %d, want 2", len(document.Spec.Resources))
	}
	if len(document.Spec.Services) != 1 {
		t.Fatalf("services = %d, want 1", len(document.Spec.Services))
	}
	service := document.Spec.Services[0]
	if service.Scale.TargetRef.Kind != "Deployment" || service.Scale.Replicas != 1 {
		t.Fatalf("service scale = %#v", service.Scale)
	}
	if err := document.Validate(); err != nil {
		t.Fatalf("default blueprint is invalid: %v", err)
	}
}

func TestMarshalAndLoadRoundTrip(t *testing.T) {
	want := Default("developer-environment")
	contents, err := Marshal(want)
	if err != nil {
		t.Fatal(err)
	}
	for _, text := range []string{
		"apiVersion: tearenv.io/v1alpha1",
		"kind: EnvironmentBlueprint",
		"nameTemplate: tearenv-{{ .IdentitySlug }}-{{ .BlueprintName }}",
		"replicas: 0",
		"idleTimeout: 10m",
	} {
		if !strings.Contains(string(contents), text) {
			t.Errorf("blueprint YAML does not contain %q:\n%s", text, contents)
		}
	}

	got, err := Load(contents)
	if err != nil {
		t.Fatal(err)
	}
	if got.Metadata.Name != want.Metadata.Name || len(got.Spec.Resources) != len(want.Spec.Resources) {
		t.Fatalf("loaded blueprint = %#v, want %#v", got, want)
	}
}

func TestInstantiateRendersAnIsolatedIdentityNamespace(t *testing.T) {
	document := Default("web-development")

	instance, err := document.Instantiate("alice")
	if err != nil {
		t.Fatal(err)
	}
	if instance.Namespace != "tearenv-alice-web-development" {
		t.Fatalf("namespace = %q, want tearenv-alice-web-development", instance.Namespace)
	}
	if instance.Labels["tearenv.io/identity"] != "alice" {
		t.Fatalf("identity label = %q, want alice", instance.Labels["tearenv.io/identity"])
	}
	for index, resource := range instance.Resources {
		metadata, _ := resource["metadata"].(map[string]any)
		if metadata["namespace"] != instance.Namespace {
			t.Errorf("resource %d namespace = %q, want %q", index, metadata["namespace"], instance.Namespace)
		}
	}
}

func TestInstantiateCreatesDistinctDNSNamesForNormalizedIdentities(t *testing.T) {
	document := Default("developer-environment")
	identities := []string{"Alice", "alice", "alice@example.com", strings.Repeat("a", 64)}
	namespaces := make(map[string]string, len(identities))
	for _, identity := range identities {
		instance, err := document.Instantiate(identity)
		if err != nil {
			t.Fatalf("Instantiate(%q): %v", identity, err)
		}
		if problems := validation.IsDNS1123Label(instance.Namespace); len(problems) != 0 {
			t.Errorf("Instantiate(%q) namespace %q is invalid: %v", identity, instance.Namespace, problems)
		}
		if previous, duplicate := namespaces[instance.Namespace]; duplicate {
			t.Errorf("identities %q and %q received namespace %q", previous, identity, instance.Namespace)
		}
		namespaces[instance.Namespace] = identity
	}
}

func TestInstantiateDoesNotMutateBlueprintResources(t *testing.T) {
	document := Default("developer-environment")

	if _, err := document.Instantiate("alice"); err != nil {
		t.Fatal(err)
	}
	metadata := document.Spec.Resources[0]["metadata"].(map[string]any)
	if _, exists := metadata["namespace"]; exists {
		t.Fatal("Instantiate added metadata.namespace to the team blueprint")
	}
}

func TestLoadRejectsUnknownBlueprintField(t *testing.T) {
	contents := []byte(`
apiVersion: tearenv.io/v1alpha1
kind: EnvironmentBlueprint
metadata:
  name: development
unexpected: true
spec:
  namespace:
    nameTemplate: tearenv-{{ .IdentitySlug }}
  resources: []
  services: []
`)

	_, err := Load(contents)
	if err == nil || !strings.Contains(err.Error(), "unexpected") {
		t.Fatalf("Load() error = %v, want unknown field error", err)
	}
}

func TestValidateRejectsInvalidBlueprints(t *testing.T) {
	tests := []struct {
		name    string
		mutate  func(*Blueprint)
		message string
	}{
		{
			name:    "api version",
			mutate:  func(document *Blueprint) { document.APIVersion = "v1" },
			message: "apiVersion",
		},
		{
			name:    "kind",
			mutate:  func(document *Blueprint) { document.Kind = "Blueprint" },
			message: "kind",
		},
		{
			name:    "metadata name",
			mutate:  func(document *Blueprint) { document.Metadata.Name = "Not Valid" },
			message: "metadata.name",
		},
		{
			name: "identity namespace",
			mutate: func(document *Blueprint) {
				document.Spec.Namespace.NameTemplate = "shared-environment"
			},
			message: "IdentitySlug",
		},
		{
			name: "blueprint namespace",
			mutate: func(document *Blueprint) {
				document.Spec.Namespace.NameTemplate = "tearenv-" + IdentitySlugTemplate
			},
			message: "BlueprintName",
		},
		{
			name: "duplicate resource",
			mutate: func(document *Blueprint) {
				document.Spec.Resources = append(document.Spec.Resources, document.Spec.Resources[0])
			},
			message: "duplicate resource",
		},
		{
			name: "resource namespace",
			mutate: func(document *Blueprint) {
				metadata := document.Spec.Resources[0]["metadata"].(map[string]any)
				metadata["namespace"] = "shared"
			},
			message: "metadata.namespace must be omitted",
		},
		{
			name: "duplicate service",
			mutate: func(document *Blueprint) {
				document.Spec.Services = append(document.Spec.Services, document.Spec.Services[0])
			},
			message: "duplicate service",
		},
		{
			name: "missing service resource",
			mutate: func(document *Blueprint) {
				document.Spec.Services[0].Target.Service = "missing"
			},
			message: "target.service",
		},
		{
			name: "missing service port",
			mutate: func(document *Blueprint) {
				document.Spec.Services[0].Target.Port = 81
			},
			message: "target.port",
		},
		{
			name: "missing scale resource",
			mutate: func(document *Blueprint) {
				document.Spec.Services[0].Scale.TargetRef.Name = "missing"
			},
			message: "scale.targetRef",
		},
		{
			name: "unsupported scalable kind",
			mutate: func(document *Blueprint) {
				document.Spec.Services[0].Scale.TargetRef.Kind = "DaemonSet"
			},
			message: "Deployment or StatefulSet",
		},
		{
			name: "workload starts active",
			mutate: func(document *Blueprint) {
				spec := document.Spec.Resources[0]["spec"].(map[string]any)
				spec["replicas"] = 1
			},
			message: "spec.replicas must be 0",
		},
		{
			name: "invalid replicas",
			mutate: func(document *Blueprint) {
				document.Spec.Services[0].Scale.Replicas = 0
			},
			message: "scale.replicas",
		},
		{
			name: "invalid ready timeout",
			mutate: func(document *Blueprint) {
				document.Spec.Services[0].Scale.ReadyTimeout = "later"
			},
			message: "readyTimeout",
		},
		{
			name: "negative idle timeout",
			mutate: func(document *Blueprint) {
				document.Spec.Services[0].Scale.IdleTimeout = "-1s"
			},
			message: "idleTimeout",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			document := Default("developer-environment")
			test.mutate(&document)
			err := document.Validate()
			if err == nil || !strings.Contains(err.Error(), test.message) {
				t.Fatalf("Validate() error = %v, want message containing %q", err, test.message)
			}
		})
	}
}
