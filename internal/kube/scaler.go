// Package kube implements a Kubernetes scaler backend.
package kube

import (
	"context"
	"errors"
	"fmt"

	"github.com/fr0stylo/tearenv/internal/scaler"
	autoscalingv1 "k8s.io/api/autoscaling/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	appsclient "k8s.io/client-go/kubernetes/typed/apps/v1"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/util/retry"
)

var _ scaler.Backend = (*Scaler)(nil)

// Scaler changes Kubernetes Deployment and StatefulSet replica counts.
type Scaler struct {
	client appsclient.AppsV1Interface
}

// NewInClusterScaler creates a scaler using the pod's service-account
// credentials and the Kubernetes API address supplied to the pod.
func NewInClusterScaler() (*Scaler, error) {
	config, err := rest.InClusterConfig()
	if err != nil {
		return nil, fmt.Errorf("load in-cluster Kubernetes config: %w", err)
	}
	client, err := appsclient.NewForConfig(config)
	if err != nil {
		return nil, fmt.Errorf("create Kubernetes client: %w", err)
	}
	return NewScaler(client), nil
}

// NewScaler constructs a Kubernetes scaler with an injected client.
func NewScaler(client appsclient.AppsV1Interface) *Scaler {
	return &Scaler{client: client}
}

// Scale updates the workload's scale subresource through client-go.
func (backend *Scaler) Scale(ctx context.Context, target scaler.Target, replicas int32) error {
	if backend == nil || backend.client == nil {
		return errors.New("Kubernetes client is required")
	}
	if target.Namespace == "" || target.Name == "" {
		return errors.New("Kubernetes workload namespace and name are required")
	}
	if replicas < 0 {
		return fmt.Errorf("Kubernetes workload replicas cannot be negative: %d", replicas)
	}

	var workloads scaleClient
	switch target.Kind {
	case "deployment":
		workloads = backend.client.Deployments(target.Namespace)
	case "statefulset":
		workloads = backend.client.StatefulSets(target.Namespace)
	default:
		return fmt.Errorf("unsupported Kubernetes workload kind %q", target.Kind)
	}

	operation := "get"
	err := retry.RetryOnConflict(retry.DefaultRetry, func() error {
		operation = "get"
		scale, err := workloads.GetScale(ctx, target.Name, metav1.GetOptions{})
		if err != nil {
			return err
		}
		scale.Spec.Replicas = replicas
		operation = "update"
		if _, err := workloads.UpdateScale(ctx, target.Name, scale, metav1.UpdateOptions{}); err != nil {
			return err
		}
		return nil
	})
	if err != nil {
		return scaleError(operation, target, err)
	}
	return nil
}

type scaleClient interface {
	GetScale(ctx context.Context, name string, options metav1.GetOptions) (*autoscalingv1.Scale, error)
	UpdateScale(ctx context.Context, name string, scale *autoscalingv1.Scale, options metav1.UpdateOptions) (*autoscalingv1.Scale, error)
}

func scaleError(operation string, target scaler.Target, err error) error {
	return fmt.Errorf("%s scale for Kubernetes %s %s/%s: %w",
		operation, target.Kind, target.Namespace, target.Name, err)
}
