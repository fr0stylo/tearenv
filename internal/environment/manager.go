// Package environment provisions team blueprints for authenticated identities.
package environment

import (
	"context"
	"errors"
	"fmt"
	"net"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/fr0stylo/tearenv/internal/authorization"
	"github.com/fr0stylo/tearenv/internal/blueprint"
)

// Reconciler applies one rendered blueprint instance to its runtime backend.
type Reconciler interface {
	Reconcile(ctx context.Context, instance blueprint.Instance) error
}

// Manager provisions one team blueprint and adds its services to the base
// identity policy after reconciliation succeeds.
type Manager struct {
	document   blueprint.Blueprint
	reconciler Reconciler
	base       authorization.Policy
	mu         sync.RWMutex
	services   map[string]map[string]authorization.Service
}

// NewManager constructs an authenticated environment manager.
func NewManager(document blueprint.Blueprint, reconciler Reconciler, base authorization.Policy) (*Manager, error) {
	if err := document.Validate(); err != nil {
		return nil, fmt.Errorf("validate environment blueprint: %w", err)
	}
	if reconciler == nil {
		return nil, errors.New("environment reconciler is required")
	}
	if base == nil {
		return nil, errors.New("base authorization policy is required")
	}
	return &Manager{
		document: document, reconciler: reconciler, base: base,
		services: make(map[string]map[string]authorization.Service),
	}, nil
}

// Provision reconciles identity's namespace and publishes the rendered service
// policy only after every Kubernetes object has been applied successfully.
func (manager *Manager) Provision(ctx context.Context, identity string) error {
	instance, err := manager.document.Instantiate(identity)
	if err != nil {
		return fmt.Errorf("instantiate environment for %q: %w", identity, err)
	}
	services, err := instanceServices(instance)
	if err != nil {
		return err
	}
	if err := manager.reconciler.Reconcile(ctx, instance); err != nil {
		return fmt.Errorf("reconcile environment %s for %q: %w", instance.Namespace, identity, err)
	}

	manager.mu.Lock()
	manager.services[identity] = services
	manager.mu.Unlock()
	return nil
}

// Services returns direct grants and services from the successfully reconciled
// identity environment. Blueprint services replace same-named direct grants.
func (manager *Manager) Services(identity string) ([]authorization.Service, error) {
	baseServices, err := manager.base.Services(identity)
	if err != nil {
		return nil, err
	}
	combined := make(map[string]authorization.Service, len(baseServices))
	for _, service := range baseServices {
		combined[service.Name] = service
	}
	manager.mu.RLock()
	for name, service := range manager.services[identity] {
		combined[name] = service
	}
	manager.mu.RUnlock()

	services := make([]authorization.Service, 0, len(combined))
	for _, service := range combined {
		services = append(services, service)
	}
	sort.Slice(services, func(left, right int) bool { return services[left].Name < services[right].Name })
	return services, nil
}

// ResolveService resolves a blueprint service before falling back to a direct grant.
func (manager *Manager) ResolveService(identity, name string) (authorization.Service, bool) {
	manager.mu.RLock()
	service, exists := manager.services[identity][name]
	manager.mu.RUnlock()
	if exists {
		return service, true
	}
	return manager.base.ResolveService(identity, name)
}

func instanceServices(instance blueprint.Instance) (map[string]authorization.Service, error) {
	services := make(map[string]authorization.Service, len(instance.Services))
	for _, declared := range instance.Services {
		readyTimeout, err := time.ParseDuration(declared.Scale.ReadyTimeout)
		if err != nil {
			return nil, fmt.Errorf("parse service %q readiness timeout: %w", declared.Name, err)
		}
		idleTimeout, err := time.ParseDuration(declared.Scale.IdleTimeout)
		if err != nil {
			return nil, fmt.Errorf("parse service %q idle timeout: %w", declared.Name, err)
		}
		targetHost := declared.Target.Service + "." + instance.Namespace + ".svc.cluster.local"
		services[declared.Name] = authorization.Service{
			Name:      declared.Name,
			Target:    net.JoinHostPort(targetHost, strconv.FormatUint(uint64(declared.Target.Port), 10)),
			LocalPort: declared.LocalPort,
			Workload: &authorization.Workload{
				Kind:         strings.ToLower(declared.Scale.TargetRef.Kind),
				Namespace:    instance.Namespace,
				Name:         declared.Scale.TargetRef.Name,
				Replicas:     declared.Scale.Replicas,
				ReadyTimeout: readyTimeout,
				IdleTimeout:  idleTimeout,
			},
		}
	}
	return services, nil
}
