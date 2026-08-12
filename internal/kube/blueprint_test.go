package kube

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/fr0stylo/tearenv/internal/blueprint"
	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

func TestBlueprintApplierServerSideAppliesNamespaceAndResources(t *testing.T) {
	patcher := &recordingResourcePatcher{}
	applier := newBlueprintApplier(patcher, testBlueprintMapper())
	instance, err := blueprint.Default("web-development").Instantiate("alice")
	if err != nil {
		t.Fatal(err)
	}

	if err := applier.Reconcile(t.Context(), instance); err != nil {
		t.Fatal(err)
	}
	if len(patcher.patches) != 3 {
		t.Fatalf("patches = %d, want namespace and two resources", len(patcher.patches))
	}
	if patcher.patches[0].resource.Resource != "namespaces" || patcher.patches[0].namespace != "" {
		t.Fatalf("first patch = %#v, want cluster-scoped namespace", patcher.patches[0])
	}
	for _, patch := range patcher.patches[1:] {
		if patch.namespace != instance.Namespace {
			t.Errorf("resource namespace = %q, want %q", patch.namespace, instance.Namespace)
		}
		var object map[string]any
		if err := json.Unmarshal(patch.contents, &object); err != nil {
			t.Fatal(err)
		}
		metadata := object["metadata"].(map[string]any)
		if metadata["namespace"] != instance.Namespace {
			t.Errorf("applied metadata.namespace = %q", metadata["namespace"])
		}
	}
}

func TestBlueprintApplierRejectsClusterScopedBlueprintResource(t *testing.T) {
	document := blueprint.Default("developer-environment")
	document.Spec.Resources = append(document.Spec.Resources, blueprint.Resource{
		"apiVersion": "rbac.authorization.k8s.io/v1",
		"kind":       "ClusterRole",
		"metadata":   map[string]any{"name": "unsafe"},
	})
	instance, err := document.Instantiate("alice")
	if err != nil {
		t.Fatal(err)
	}
	mapper := testBlueprintMapper()
	mapper.Add(schema.GroupVersionKind{Group: "rbac.authorization.k8s.io", Version: "v1", Kind: "ClusterRole"}, meta.RESTScopeRoot)

	err = newBlueprintApplier(&recordingResourcePatcher{}, mapper).Reconcile(t.Context(), instance)
	if err == nil || !strings.Contains(err.Error(), "must be namespace-scoped") {
		t.Fatalf("Reconcile() error = %v", err)
	}
}

type resourcePatch struct {
	resource  schema.GroupVersionResource
	namespace string
	name      string
	contents  []byte
}

type recordingResourcePatcher struct {
	patches []resourcePatch
}

func (patcher *recordingResourcePatcher) Apply(
	_ context.Context,
	resource schema.GroupVersionResource,
	namespace string,
	name string,
	contents []byte,
) error {
	patcher.patches = append(patcher.patches, resourcePatch{
		resource: resource, namespace: namespace, name: name, contents: append([]byte(nil), contents...),
	})
	return nil
}

func testBlueprintMapper() *meta.DefaultRESTMapper {
	mapper := meta.NewDefaultRESTMapper([]schema.GroupVersion{
		{Version: "v1"},
		{Group: "apps", Version: "v1"},
	})
	mapper.Add(schema.GroupVersionKind{Version: "v1", Kind: "Service"}, meta.RESTScopeNamespace)
	mapper.Add(schema.GroupVersionKind{Group: "apps", Version: "v1", Kind: "Deployment"}, meta.RESTScopeNamespace)
	mapper.Add(schema.GroupVersionKind{Group: "apps", Version: "v1", Kind: "StatefulSet"}, meta.RESTScopeNamespace)
	return mapper
}
