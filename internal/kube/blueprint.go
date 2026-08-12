package kube

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/fr0stylo/tearenv/internal/blueprint"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/discovery"
	"k8s.io/client-go/discovery/cached/memory"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/restmapper"
)

const blueprintFieldManager = "tearenv-blueprint"

var namespaceResource = schema.GroupVersionResource{Version: "v1", Resource: "namespaces"}

// BlueprintApplier reconciles rendered namespace-scoped Kubernetes resources
// with server-side apply.
type BlueprintApplier struct {
	patcher resourcePatcher
	mapper  meta.RESTMapper
}

type resourcePatcher interface {
	Apply(ctx context.Context, resource schema.GroupVersionResource, namespace, name string, contents []byte) error
}

type dynamicResourcePatcher struct {
	client dynamic.Interface
}

type applyOperation struct {
	resource  schema.GroupVersionResource
	namespace string
	name      string
	contents  []byte
}

// NewInClusterBlueprintApplier creates an applier from pod service-account credentials.
func NewInClusterBlueprintApplier() (*BlueprintApplier, error) {
	config, err := rest.InClusterConfig()
	if err != nil {
		return nil, fmt.Errorf("load in-cluster Kubernetes config: %w", err)
	}
	client, err := dynamic.NewForConfig(config)
	if err != nil {
		return nil, fmt.Errorf("create dynamic Kubernetes client: %w", err)
	}
	discoveryClient, err := discovery.NewDiscoveryClientForConfig(config)
	if err != nil {
		return nil, fmt.Errorf("create Kubernetes discovery client: %w", err)
	}
	mapper := restmapper.NewDeferredDiscoveryRESTMapper(memory.NewMemCacheClient(discoveryClient))
	return NewBlueprintApplier(client, mapper), nil
}

// NewBlueprintApplier creates a blueprint applier with injected Kubernetes clients.
func NewBlueprintApplier(client dynamic.Interface, mapper meta.RESTMapper) *BlueprintApplier {
	if client == nil {
		return &BlueprintApplier{mapper: mapper}
	}
	return newBlueprintApplier(&dynamicResourcePatcher{client: client}, mapper)
}

func newBlueprintApplier(patcher resourcePatcher, mapper meta.RESTMapper) *BlueprintApplier {
	return &BlueprintApplier{patcher: patcher, mapper: mapper}
}

// Reconcile applies the namespace followed by every rendered resource.
func (applier *BlueprintApplier) Reconcile(ctx context.Context, instance blueprint.Instance) error {
	if applier == nil || applier.patcher == nil || applier.mapper == nil {
		return errors.New("kubernetes blueprint client and REST mapper are required")
	}
	operations, err := applier.operations(instance)
	if err != nil {
		return err
	}
	for _, operation := range operations {
		if err := applier.patcher.Apply(
			ctx, operation.resource, operation.namespace, operation.name, operation.contents,
		); err != nil {
			return fmt.Errorf("apply Kubernetes resource %s %s/%s: %w",
				operation.resource.Resource, operation.namespace, operation.name, err)
		}
	}
	return nil
}

func (applier *BlueprintApplier) operations(instance blueprint.Instance) ([]applyOperation, error) {
	namespaceContents, err := json.Marshal(map[string]any{
		"apiVersion": "v1",
		"kind":       "Namespace",
		"metadata": map[string]any{
			"name":   instance.Namespace,
			"labels": instance.Labels,
		},
	})
	if err != nil {
		return nil, fmt.Errorf("encode namespace %q: %w", instance.Namespace, err)
	}
	operations := make([]applyOperation, 0, len(instance.Resources)+1)
	operations = append(operations, applyOperation{
		resource: namespaceResource, name: instance.Namespace, contents: namespaceContents,
	})
	for index, resource := range instance.Resources {
		apiVersion, _ := resource["apiVersion"].(string)
		kind, _ := resource["kind"].(string)
		metadata, _ := resource["metadata"].(map[string]any)
		name, _ := metadata["name"].(string)
		groupVersion, err := schema.ParseGroupVersion(apiVersion)
		if err != nil {
			return nil, fmt.Errorf("map spec.resources[%d] apiVersion %q: %w", index, apiVersion, err)
		}
		mapping, err := applier.mapper.RESTMapping(schema.GroupKind{Group: groupVersion.Group, Kind: kind}, groupVersion.Version)
		if err != nil {
			return nil, fmt.Errorf("map spec.resources[%d] %s %s: %w", index, apiVersion, kind, err)
		}
		if mapping.Scope.Name() != meta.RESTScopeNameNamespace {
			return nil, fmt.Errorf("spec.resources[%d] %s %s must be namespace-scoped", index, apiVersion, kind)
		}
		contents, err := json.Marshal(resource)
		if err != nil {
			return nil, fmt.Errorf("encode spec.resources[%d] %s %q: %w", index, kind, name, err)
		}
		operations = append(operations, applyOperation{
			resource: mapping.Resource, namespace: instance.Namespace, name: name, contents: contents,
		})
	}
	return operations, nil
}

func (patcher *dynamicResourcePatcher) Apply(
	ctx context.Context,
	resource schema.GroupVersionResource,
	namespace string,
	name string,
	contents []byte,
) error {
	resources := patcher.client.Resource(resource)
	var target dynamic.ResourceInterface = resources
	if namespace != "" {
		target = resources.Namespace(namespace)
	}
	force := true
	if _, err := target.Patch(ctx, name, types.ApplyPatchType, contents, metav1.PatchOptions{
		FieldManager: blueprintFieldManager,
		Force:        &force,
	}); err != nil {
		return err
	}
	return nil
}
