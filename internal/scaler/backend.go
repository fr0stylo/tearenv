// Package scaler defines the boundary between service lifecycle management and
// the runtime that starts and stops workloads.
package scaler

import "context"

// Target identifies a workload in a scaler backend. Kind and Namespace are
// interpreted by the backend, so implementations can model Kubernetes
// workloads, Docker containers, containerd tasks, or other runtimes.
type Target struct {
	Kind      string
	Namespace string
	Name      string
}

// Backend changes the desired replica count for a workload target.
// Implementations that manage a single process may treat zero as stopped and a
// positive value as running.
type Backend interface {
	Scale(ctx context.Context, target Target, replicas int32) error
}
