// Package blueprint defines versioned, team-owned environment templates.
package blueprint

import (
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	"k8s.io/apimachinery/pkg/util/validation"
	"sigs.k8s.io/yaml"
)

const (
	// APIVersion is the first supported environment blueprint API version.
	APIVersion = "tearenv.io/v1alpha1"
	// Kind identifies an environment blueprint document.
	Kind = "EnvironmentBlueprint"
	// IdentitySlugTemplate is replaced with a DNS-safe identity during provisioning.
	IdentitySlugTemplate = "{{ .IdentitySlug }}"
	// BlueprintNameTemplate is replaced with the selected blueprint name during provisioning.
	BlueprintNameTemplate = "{{ .BlueprintName }}"
)

var validServiceName = regexp.MustCompile(`^[a-z][a-z0-9-]{0,31}$`)

// Blueprint describes reusable Kubernetes resources and exposed services that
// a team makes available for authenticated identities to instantiate.
type Blueprint struct {
	APIVersion string        `json:"apiVersion"`
	Kind       string        `json:"kind"`
	Metadata   Metadata      `json:"metadata"`
	Spec       BlueprintSpec `json:"spec"`
}

// Metadata identifies a blueprint independently of an instantiated environment.
type Metadata struct {
	Name string `json:"name"`
}

// BlueprintSpec contains the per-identity namespace, resource templates, and
// service lifecycle declarations.
type BlueprintSpec struct {
	Namespace NamespaceTemplate `json:"namespace"`
	Resources []Resource        `json:"resources"`
	Services  []Service         `json:"services"`
}

// NamespaceTemplate determines the namespace used for each identity.
type NamespaceTemplate struct {
	NameTemplate string            `json:"nameTemplate"`
	Labels       map[string]string `json:"labels,omitempty"`
}

// Resource is a Kubernetes-style resource template. Provisioning injects the
// rendered identity namespace, so resources must not set metadata.namespace.
type Resource map[string]any

// Service exposes one resource through tearenv and defines its scale lifecycle.
type Service struct {
	Name      string        `json:"name"`
	LocalPort uint32        `json:"localPort,omitempty"`
	Target    ServiceTarget `json:"target"`
	Scale     ScalePolicy   `json:"scale"`
}

// ServiceTarget identifies a Kubernetes Service in the instantiated namespace.
type ServiceTarget struct {
	Service string `json:"service"`
	Port    uint32 `json:"port"`
}

// ScalePolicy identifies the workload and its active and idle behavior.
type ScalePolicy struct {
	TargetRef    WorkloadReference `json:"targetRef"`
	Replicas     int32             `json:"replicas"`
	ReadyTimeout string            `json:"readyTimeout"`
	IdleTimeout  string            `json:"idleTimeout"`
}

// WorkloadReference identifies a scalable workload in the environment namespace.
type WorkloadReference struct {
	APIVersion string `json:"apiVersion"`
	Kind       string `json:"kind"`
	Name       string `json:"name"`
}

// Default returns a complete starter blueprint with one scale-to-zero web service.
func Default(name string) Blueprint {
	return Blueprint{
		APIVersion: APIVersion,
		Kind:       Kind,
		Metadata:   Metadata{Name: name},
		Spec: BlueprintSpec{
			Namespace: NamespaceTemplate{
				NameTemplate: "tearenv-" + IdentitySlugTemplate + "-" + BlueprintNameTemplate,
				Labels: map[string]string{
					"app.kubernetes.io/managed-by": "tearenv",
					"tearenv.io/identity":          IdentitySlugTemplate,
					"tearenv.io/blueprint":         BlueprintNameTemplate,
				},
			},
			Resources: []Resource{
				{
					"apiVersion": "apps/v1",
					"kind":       "Deployment",
					"metadata": map[string]any{
						"name":   "workspace",
						"labels": workspaceLabels(),
					},
					"spec": map[string]any{
						"replicas": 0,
						"selector": map[string]any{"matchLabels": workspaceLabels()},
						"template": map[string]any{
							"metadata": map[string]any{"labels": workspaceLabels()},
							"spec": map[string]any{
								"containers": []any{map[string]any{
									"name":  "web",
									"image": "nginx:1.27-alpine",
									"ports": []any{map[string]any{"name": "http", "containerPort": 80}},
								}},
							},
						},
					},
				},
				{
					"apiVersion": "v1",
					"kind":       "Service",
					"metadata": map[string]any{
						"name": "workspace",
					},
					"spec": map[string]any{
						"selector": workspaceLabels(),
						"ports":    []any{map[string]any{"name": "http", "port": 80, "targetPort": "http"}},
					},
				},
			},
			Services: []Service{
				{
					Name:      "web",
					LocalPort: 8080,
					Target:    ServiceTarget{Service: "workspace", Port: 80},
					Scale: ScalePolicy{
						TargetRef: WorkloadReference{APIVersion: "apps/v1", Kind: "Deployment", Name: "workspace"},
						Replicas:  1, ReadyTimeout: "2m", IdleTimeout: "10m",
					},
				},
			},
		},
	}
}

func workspaceLabels() map[string]any {
	return map[string]any{"app.kubernetes.io/name": "workspace"}
}

// Marshal validates and renders a blueprint as YAML.
func Marshal(document Blueprint) ([]byte, error) {
	if err := document.Validate(); err != nil {
		return nil, err
	}
	contents, err := yaml.Marshal(document)
	if err != nil {
		return nil, fmt.Errorf("marshal environment blueprint: %w", err)
	}
	return contents, nil
}

// Load strictly decodes and validates a YAML or JSON blueprint document.
func Load(contents []byte) (Blueprint, error) {
	var document Blueprint
	if err := yaml.UnmarshalStrict(contents, &document); err != nil {
		return Blueprint{}, fmt.Errorf("parse environment blueprint: %w", err)
	}
	if err := document.Validate(); err != nil {
		return Blueprint{}, err
	}
	return document, nil
}

// Validate checks the blueprint's version, isolation boundary, resource
// references, and scale lifecycle values.
func (document Blueprint) Validate() error {
	if document.APIVersion != APIVersion {
		return fmt.Errorf("apiVersion must be %q", APIVersion)
	}
	if document.Kind != Kind {
		return fmt.Errorf("kind must be %q", Kind)
	}
	if problems := validation.IsDNS1123Label(document.Metadata.Name); len(problems) != 0 {
		return fmt.Errorf("metadata.name %q is invalid: %s", document.Metadata.Name, strings.Join(problems, "; "))
	}
	if err := validateNamespace(document.Spec.Namespace); err != nil {
		return err
	}
	resources, err := validateResources(document.Spec.Resources)
	if err != nil {
		return err
	}
	return validateServices(document.Spec.Services, resources)
}

type resourceCatalog map[string]Resource

func validateNamespace(namespace NamespaceTemplate) error {
	if strings.Count(namespace.NameTemplate, IdentitySlugTemplate) != 1 {
		return fmt.Errorf("spec.namespace.nameTemplate must contain %q exactly once", IdentitySlugTemplate)
	}
	if strings.Count(namespace.NameTemplate, BlueprintNameTemplate) != 1 {
		return fmt.Errorf("spec.namespace.nameTemplate must contain %q exactly once", BlueprintNameTemplate)
	}
	renderedName := renderTemplateValue(namespace.NameTemplate)
	if problems := validation.IsDNS1123Label(renderedName); len(problems) != 0 {
		return fmt.Errorf("spec.namespace.nameTemplate %q is invalid: %s", namespace.NameTemplate, strings.Join(problems, "; "))
	}
	for key, value := range namespace.Labels {
		if problems := validation.IsQualifiedName(key); len(problems) != 0 {
			return fmt.Errorf("spec.namespace.labels key %q is invalid: %s", key, strings.Join(problems, "; "))
		}
		renderedValue := renderTemplateValue(value)
		if problems := validation.IsValidLabelValue(renderedValue); len(problems) != 0 {
			return fmt.Errorf("spec.namespace.labels[%q] is invalid: %s", key, strings.Join(problems, "; "))
		}
	}
	return nil
}

func renderTemplateValue(value string) string {
	value = strings.ReplaceAll(value, IdentitySlugTemplate, "identity")
	return strings.ReplaceAll(value, BlueprintNameTemplate, "blueprint")
}

func validateResources(resources []Resource) (resourceCatalog, error) {
	if len(resources) == 0 {
		return nil, errors.New("spec.resources must contain at least one Kubernetes resource")
	}
	catalog := make(resourceCatalog, len(resources))
	for index, resource := range resources {
		apiVersion, kind, name, err := resourceIdentity(resource)
		if err != nil {
			return nil, fmt.Errorf("spec.resources[%d]: %w", index, err)
		}
		identity := resourceKey(apiVersion, kind, name)
		if _, duplicate := catalog[identity]; duplicate {
			return nil, fmt.Errorf("spec.resources[%d]: duplicate resource %s %s/%s", index, apiVersion, kind, name)
		}
		catalog[identity] = resource
	}
	return catalog, nil
}

func resourceIdentity(resource Resource) (string, string, string, error) {
	apiVersion, _ := resource["apiVersion"].(string)
	kind, _ := resource["kind"].(string)
	metadata, _ := resource["metadata"].(map[string]any)
	name, _ := metadata["name"].(string)
	if apiVersion == "" || kind == "" || name == "" {
		return "", "", "", errors.New("apiVersion, kind, and metadata.name are required")
	}
	if namespace, _ := metadata["namespace"].(string); namespace != "" {
		return "", "", "", errors.New("metadata.namespace must be omitted; tearenv injects the identity namespace")
	}
	if problems := validation.IsDNS1123Subdomain(name); len(problems) != 0 {
		return "", "", "", fmt.Errorf("metadata.name %q is invalid: %s", name, strings.Join(problems, "; "))
	}
	return apiVersion, kind, name, nil
}

func validateServices(services []Service, resources resourceCatalog) error {
	if len(services) == 0 {
		return errors.New("spec.services must contain at least one exposed service")
	}
	names := make(map[string]struct{}, len(services))
	for index, service := range services {
		path := fmt.Sprintf("spec.services[%d]", index)
		if !validServiceName.MatchString(service.Name) {
			return fmt.Errorf("%s.name %q must match %s", path, service.Name, validServiceName.String())
		}
		if _, duplicate := names[service.Name]; duplicate {
			return fmt.Errorf("%s.name: duplicate service %q", path, service.Name)
		}
		names[service.Name] = struct{}{}
		if service.Target.Port == 0 || service.Target.Port > 65535 {
			return fmt.Errorf("%s.target.port must be between 1 and 65535", path)
		}
		if service.LocalPort > 65535 {
			return fmt.Errorf("%s.localPort must be between 1 and 65535", path)
		}
		serviceResource, exists := resources[resourceKey("v1", "Service", service.Target.Service)]
		if !exists {
			return fmt.Errorf("%s.target.service %q does not reference a v1 Service resource", path, service.Target.Service)
		}
		if !resourceExposesPort(serviceResource, service.Target.Port) {
			return fmt.Errorf("%s.target.port %d is not exposed by Service %q", path, service.Target.Port, service.Target.Service)
		}
		if err := validateScale(path, service.Scale, resources); err != nil {
			return err
		}
	}
	return nil
}

func validateScale(path string, scale ScalePolicy, resources resourceCatalog) error {
	if scale.TargetRef.APIVersion != "apps/v1" || (scale.TargetRef.Kind != "Deployment" && scale.TargetRef.Kind != "StatefulSet") {
		return fmt.Errorf("%s.scale.targetRef must reference an apps/v1 Deployment or StatefulSet", path)
	}
	workload, exists := resources[resourceKey(scale.TargetRef.APIVersion, scale.TargetRef.Kind, scale.TargetRef.Name)]
	if !exists {
		return fmt.Errorf("%s.scale.targetRef does not reference a resource in spec.resources", path)
	}
	if replicas, exists := nestedNumber(workload, "spec", "replicas"); !exists || replicas != 0 {
		return fmt.Errorf("%s.scale.targetRef resource spec.replicas must be 0", path)
	}
	if scale.Replicas < 1 {
		return fmt.Errorf("%s.scale.replicas must be at least 1", path)
	}
	readyTimeout, err := time.ParseDuration(scale.ReadyTimeout)
	if err != nil || readyTimeout <= 0 {
		return fmt.Errorf("%s.scale.readyTimeout must be a positive duration", path)
	}
	idleTimeout, err := time.ParseDuration(scale.IdleTimeout)
	if err != nil || idleTimeout < 0 {
		return fmt.Errorf("%s.scale.idleTimeout must be a non-negative duration", path)
	}
	return nil
}

func resourceExposesPort(resource Resource, expected uint32) bool {
	spec, _ := resource["spec"].(map[string]any)
	ports, _ := spec["ports"].([]any)
	for _, item := range ports {
		port, _ := item.(map[string]any)
		value, exists := number(port["port"])
		if exists && value == int64(expected) {
			return true
		}
	}
	return false
}

func nestedNumber(resource Resource, objectKey, valueKey string) (int64, bool) {
	object, _ := resource[objectKey].(map[string]any)
	return number(object[valueKey])
}

func number(value any) (int64, bool) {
	switch typed := value.(type) {
	case int:
		return int64(typed), true
	case int32:
		return int64(typed), true
	case int64:
		return typed, true
	case float64:
		converted := int64(typed)
		return converted, float64(converted) == typed
	default:
		return 0, false
	}
}

func resourceKey(apiVersion, kind, name string) string {
	return apiVersion + "\x00" + kind + "\x00" + name
}
