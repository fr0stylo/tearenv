package kube

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/fr0stylo/tearenv/internal/scaler"
	autoscalingv1 "k8s.io/api/autoscaling/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	fakeapps "k8s.io/client-go/kubernetes/typed/apps/v1/fake"
	clienttesting "k8s.io/client-go/testing"
)

func TestScalerUpdatesWorkloadScale(t *testing.T) {
	tests := []struct {
		name     string
		kind     string
		resource string
	}{
		{name: "deployment", kind: "deployment", resource: "deployments"},
		{name: "stateful set", kind: "statefulset", resource: "statefulsets"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			client := &fakeapps.FakeAppsV1{Fake: &clienttesting.Fake{}}
			client.PrependReactor("get", test.resource, func(action clienttesting.Action) (bool, runtime.Object, error) {
				if action.GetSubresource() != "scale" {
					t.Fatalf("get subresource = %q, want scale", action.GetSubresource())
				}
				get := action.(clienttesting.GetAction)
				if get.GetNamespace() != "dev-alice" || get.GetName() != "postgres" {
					t.Fatalf("get target = %s/%s, want dev-alice/postgres", get.GetNamespace(), get.GetName())
				}
				return true, &autoscalingv1.Scale{
					ObjectMeta: metav1.ObjectMeta{Name: "postgres", Namespace: "dev-alice", ResourceVersion: "7"},
					Spec:       autoscalingv1.ScaleSpec{Replicas: 0},
				}, nil
			})
			client.PrependReactor("update", test.resource, func(action clienttesting.Action) (bool, runtime.Object, error) {
				if action.GetSubresource() != "scale" {
					t.Fatalf("update subresource = %q, want scale", action.GetSubresource())
				}
				updated := action.(clienttesting.UpdateAction).GetObject().(*autoscalingv1.Scale)
				if updated.Spec.Replicas != 2 {
					t.Fatalf("replicas = %d, want 2", updated.Spec.Replicas)
				}
				if updated.ResourceVersion != "7" {
					t.Fatalf("resource version = %q, want 7", updated.ResourceVersion)
				}
				return true, updated, nil
			})

			backend := NewScaler(client)
			err := backend.Scale(context.Background(), scaler.Target{
				Kind: test.kind, Namespace: "dev-alice", Name: "postgres",
			}, 2)
			if err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestScalerRejectsUnsupportedWorkloadKind(t *testing.T) {
	backend := NewScaler(&fakeapps.FakeAppsV1{Fake: &clienttesting.Fake{}})
	err := backend.Scale(context.Background(), scaler.Target{
		Kind: "daemonset", Namespace: "dev-alice", Name: "agent",
	}, 1)
	if err == nil || !strings.Contains(err.Error(), `unsupported Kubernetes workload kind "daemonset"`) {
		t.Fatalf("Scale() error = %v", err)
	}
}

func TestScalerWrapsKubernetesErrors(t *testing.T) {
	want := errors.New("API unavailable")
	client := &fakeapps.FakeAppsV1{Fake: &clienttesting.Fake{}}
	client.PrependReactor("get", "deployments", func(clienttesting.Action) (bool, runtime.Object, error) {
		return true, nil, want
	})

	err := NewScaler(client).Scale(context.Background(), scaler.Target{
		Kind: "deployment", Namespace: "dev-alice", Name: "api",
	}, 1)
	if !errors.Is(err, want) {
		t.Fatalf("Scale() error = %v, want wrapped API error", err)
	}
}

func TestScalerRetriesScaleUpdateConflicts(t *testing.T) {
	client := &fakeapps.FakeAppsV1{Fake: &clienttesting.Fake{}}
	client.PrependReactor("get", "deployments", func(clienttesting.Action) (bool, runtime.Object, error) {
		return true, &autoscalingv1.Scale{
			ObjectMeta: metav1.ObjectMeta{Name: "api", Namespace: "dev-alice", ResourceVersion: "7"},
		}, nil
	})
	updates := 0
	client.PrependReactor("update", "deployments", func(action clienttesting.Action) (bool, runtime.Object, error) {
		updates++
		if updates == 1 {
			return true, nil, apierrors.NewConflict(
				schema.GroupResource{Group: "apps", Resource: "deployments"},
				"api",
				errors.New("resource version changed"),
			)
		}
		return true, action.(clienttesting.UpdateAction).GetObject(), nil
	})

	err := NewScaler(client).Scale(context.Background(), scaler.Target{
		Kind: "deployment", Namespace: "dev-alice", Name: "api",
	}, 3)
	if err != nil {
		t.Fatal(err)
	}
	if updates != 2 {
		t.Fatalf("update attempts = %d, want 2", updates)
	}
}
