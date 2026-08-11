package kube

import (
	"context"
	"errors"
	"fmt"

	"github.com/fr0stylo/tearenv/internal/authorization"
	"golang.org/x/crypto/ssh"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	coreclient "k8s.io/client-go/kubernetes/typed/core/v1"
	"k8s.io/client-go/tools/clientcmd"
	"k8s.io/client-go/util/retry"
)

const defaultFieldManager = "tearenv"

// AuthorizedKeyOptions identifies the Kubernetes Secret updated during
// public-key login.
type AuthorizedKeyOptions struct {
	Kubeconfig string
	Context    string
	Namespace  string
	Secret     string
	Identity   string
}

type secretClient interface {
	Get(ctx context.Context, name string, options metav1.GetOptions) (*corev1.Secret, error)
	Create(ctx context.Context, secret *corev1.Secret, options metav1.CreateOptions) (*corev1.Secret, error)
	Update(ctx context.Context, secret *corev1.Secret, options metav1.UpdateOptions) (*corev1.Secret, error)
}

// RegisterAuthorizedKey adds a public key to the selected Kubernetes Secret
// using the caller's kubeconfig credentials.
func RegisterAuthorizedKey(ctx context.Context, options AuthorizedKeyOptions, key ssh.PublicKey) error {
	if options.Namespace == "" {
		return errors.New("kubernetes namespace is required")
	}
	if options.Secret == "" {
		return errors.New("kubernetes Secret name is required")
	}
	loadingRules := clientcmd.NewDefaultClientConfigLoadingRules()
	if options.Kubeconfig != "" {
		loadingRules.ExplicitPath = options.Kubeconfig
	}
	overrides := &clientcmd.ConfigOverrides{CurrentContext: options.Context}
	config, err := clientcmd.NewNonInteractiveDeferredLoadingClientConfig(loadingRules, overrides).ClientConfig()
	if err != nil {
		return fmt.Errorf("load Kubernetes client config: %w", err)
	}
	clientset, err := kubernetes.NewForConfig(config)
	if err != nil {
		return fmt.Errorf("create Kubernetes client: %w", err)
	}
	return UpsertAuthorizedKeySecret(ctx, clientset.CoreV1().Secrets(options.Namespace), options.Secret, options.Identity, key)
}

// UpsertAuthorizedKeySecret updates the public-key document without replacing
// keys belonging to other identities.
func UpsertAuthorizedKeySecret(ctx context.Context, secrets secretClient, name, identity string, key ssh.PublicKey) error {
	if secrets == nil {
		return errors.New("kubernetes Secret client is required")
	}
	err := retry.RetryOnConflict(retry.DefaultRetry, func() error {
		secret, err := secrets.Get(ctx, name, metav1.GetOptions{})
		if apierrors.IsNotFound(err) {
			contents, updateErr := authorization.UpsertPublicKey(nil, identity, key)
			if updateErr != nil {
				return updateErr
			}
			_, createErr := secrets.Create(ctx, &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{Name: name},
				Type:       corev1.SecretTypeOpaque,
				Data:       map[string][]byte{authorization.PublicKeysDataKey: contents},
			}, metav1.CreateOptions{FieldManager: defaultFieldManager})
			if createErr != nil {
				return fmt.Errorf("create authorized keys Secret %q: %w", name, createErr)
			}
			return nil
		}
		if err != nil {
			return fmt.Errorf("get authorized keys Secret %q: %w", name, err)
		}
		contents, err := authorization.UpsertPublicKey(secret.Data[authorization.PublicKeysDataKey], identity, key)
		if err != nil {
			return err
		}
		if secret.Data == nil {
			secret.Data = make(map[string][]byte)
		}
		secret.Data[authorization.PublicKeysDataKey] = contents
		if _, err := secrets.Update(ctx, secret, metav1.UpdateOptions{FieldManager: defaultFieldManager}); err != nil {
			if apierrors.IsConflict(err) {
				return err
			}
			return fmt.Errorf("update authorized keys Secret %q: %w", name, err)
		}
		return nil
	})
	if err != nil {
		return fmt.Errorf("upsert authorized key in Secret %q: %w", name, err)
	}
	return nil
}

var _ secretClient = (coreclient.SecretInterface)(nil)
