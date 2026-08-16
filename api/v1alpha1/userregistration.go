package v1alpha1

import (
	"bytes"
	"crypto/sha256"
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"strings"

	"golang.org/x/crypto/ssh"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/validation"
	"sigs.k8s.io/yaml"
)

const (
	// Group is the API group shared by tearenv resources.
	Group = "tearenv.io"
	// Version is the experimental API version.
	Version = "v1alpha1"
	// APIVersion identifies this version of the tearenv API.
	APIVersion = Group + "/" + Version
	// UserRegistrationKind identifies a user registration resource.
	UserRegistrationKind = "UserRegistration"
	// UserRegistrationListKind identifies a list of user registrations.
	UserRegistrationListKind = "UserRegistrationList"
	// UserRegistrationResource is the plural resource name used in API paths.
	UserRegistrationResource = "userregistrations"
	// ConditionAccepted reports whether the API has authorized a registration.
	ConditionAccepted = "Accepted"
	// AuthenticationMethodOIDC identifies a registration bound to an OIDC subject.
	AuthenticationMethodOIDC = "oidc"
)

var validIdentity = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._@-]{0,63}$`)
var invalidResourceName = regexp.MustCompile(`[^a-z0-9.-]+`)

// UserRegistration declares the public credentials that may authenticate as
// one tearenv identity. It is an API document only; no controller consumes it
// yet.
type UserRegistration struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   UserRegistrationSpec    `json:"spec"`
	Status *UserRegistrationStatus `json:"status,omitempty"`
}

// UserRegistrationList is the collection form of UserRegistration.
type UserRegistrationList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`

	Items []UserRegistration `json:"items"`
}

// UserRegistrationSpec is the desired registration state. It contains public
// material only. Private keys, tokens, and invites must never be stored here.
type UserRegistrationSpec struct {
	Identity   string         `json:"identity"`
	PublicKeys []SSHPublicKey `json:"publicKeys"`
}

// SSHPublicKey gives one OpenSSH public key a stable name for future credential
// management without handling private key material.
type SSHPublicKey struct {
	Name string `json:"name"`
	Key  string `json:"key"`
}

// UserRegistrationStatus is owned by the API server or a future controller.
// Producers of registration requests should leave it unset.
type UserRegistrationStatus struct {
	ObservedGeneration     int64                   `json:"observedGeneration,omitempty"`
	AuthenticatedPrincipal *AuthenticatedPrincipal `json:"authenticatedPrincipal,omitempty"`
	Conditions             []metav1.Condition      `json:"conditions,omitempty"`
}

// AuthenticatedPrincipal is the server-owned identity that created or adopted
// a registration. Issuer and subject are public identifiers, not credentials.
type AuthenticatedPrincipal struct {
	Method  string `json:"method"`
	Issuer  string `json:"issuer"`
	Subject string `json:"subject"`
}

// ResourceName derives a stable Kubernetes-compatible metadata or key name
// from an identity or hostname.
func ResourceName(value string) string {
	original := strings.TrimSpace(value)
	normalized := strings.ToLower(original)
	normalized = invalidResourceName.ReplaceAllString(normalized, "-")
	normalized = strings.Trim(normalized, ".-")
	if normalized == "" {
		normalized = "registration"
	}
	if normalized == original && len(normalized) <= validation.DNS1123SubdomainMaxLength && len(validation.IsDNS1123Subdomain(normalized)) == 0 {
		return normalized
	}
	digest := fmt.Sprintf("%x", sha256.Sum256([]byte(value)))[:12]
	maximumPrefix := validation.DNS1123SubdomainMaxLength - len(digest) - 1
	if len(normalized) > maximumPrefix {
		normalized = normalized[:maximumPrefix]
	}
	normalized = strings.TrimRight(normalized, ".-")
	return normalized + "-" + digest
}

// KeyName derives a stable DNS label for one locally generated credential.
func KeyName(value string) string {
	name := strings.ReplaceAll(ResourceName(value), ".", "-")
	if len(name) <= validation.DNS1123LabelMaxLength {
		return name
	}
	digest := fmt.Sprintf("%x", sha256.Sum256([]byte(value)))[:12]
	prefix := strings.TrimRight(name[:validation.DNS1123LabelMaxLength-len(digest)-1], "-")
	return prefix + "-" + digest
}

// Accepted reports the latest Accepted condition written by the API.
func (registration UserRegistration) Accepted() bool {
	condition := registration.AcceptanceCondition()
	return condition != nil && condition.Status == metav1.ConditionTrue
}

// AcceptanceCondition returns the API-owned Accepted condition, when present.
func (registration UserRegistration) AcceptanceCondition() *metav1.Condition {
	if registration.Status == nil {
		return nil
	}
	return apimeta.FindStatusCondition(registration.Status.Conditions, ConditionAccepted)
}

// MarshalUserRegistration validates and encodes one registration as YAML.
func MarshalUserRegistration(registration UserRegistration) ([]byte, error) {
	if err := registration.Validate(); err != nil {
		return nil, err
	}
	contents, err := yaml.Marshal(registration)
	if err != nil {
		return nil, fmt.Errorf("marshal user registration: %w", err)
	}
	return contents, nil
}

// LoadUserRegistration strictly decodes and validates one YAML or JSON
// registration document.
func LoadUserRegistration(contents []byte) (UserRegistration, error) {
	var registration UserRegistration
	if err := yaml.UnmarshalStrict(contents, &registration); err != nil {
		return UserRegistration{}, fmt.Errorf("parse user registration: %w", err)
	}
	if err := registration.Validate(); err != nil {
		return UserRegistration{}, err
	}
	return registration, nil
}

// Validate checks the versioned API envelope and registration fields.
func (registration UserRegistration) Validate() error {
	if registration.APIVersion != APIVersion {
		return fmt.Errorf("apiVersion must be %q", APIVersion)
	}
	if registration.Kind != UserRegistrationKind {
		return fmt.Errorf("kind must be %q", UserRegistrationKind)
	}
	if problems := validation.IsDNS1123Subdomain(registration.Name); len(problems) != 0 {
		return fmt.Errorf("metadata.name %q is invalid: %s", registration.Name, strings.Join(problems, "; "))
	}
	if registration.Namespace != "" {
		if problems := validation.IsDNS1123Label(registration.Namespace); len(problems) != 0 {
			return fmt.Errorf("metadata.namespace %q is invalid: %s", registration.Namespace, strings.Join(problems, "; "))
		}
	}
	if !validIdentity.MatchString(registration.Spec.Identity) {
		return fmt.Errorf("spec.identity %q must match %s", registration.Spec.Identity, validIdentity)
	}
	if expected := ResourceName(registration.Spec.Identity); registration.Name != expected {
		return fmt.Errorf("metadata.name must be %q for spec.identity %q", expected, registration.Spec.Identity)
	}
	if err := validateAuthenticatedPrincipal(registration.Status); err != nil {
		return err
	}
	return validatePublicKeys(registration.Spec.PublicKeys)
}

func validateAuthenticatedPrincipal(status *UserRegistrationStatus) error {
	if status == nil || status.AuthenticatedPrincipal == nil {
		return nil
	}
	principal := status.AuthenticatedPrincipal
	if principal.Method != AuthenticationMethodOIDC {
		return fmt.Errorf("status.authenticatedPrincipal.method must be %q", AuthenticationMethodOIDC)
	}
	issuer, err := url.Parse(principal.Issuer)
	if err != nil || issuer.Scheme != "https" || issuer.Host == "" || issuer.RawQuery != "" || issuer.Fragment != "" {
		return errors.New("status.authenticatedPrincipal.issuer must be an HTTPS URL without query or fragment")
	}
	if strings.TrimSpace(principal.Subject) == "" {
		return errors.New("status.authenticatedPrincipal.subject is required")
	}
	return nil
}

func validatePublicKeys(publicKeys []SSHPublicKey) error {
	if len(publicKeys) == 0 {
		return errors.New("spec.publicKeys must contain at least one SSH public key")
	}
	names := make(map[string]struct{}, len(publicKeys))
	keys := make(map[string]struct{}, len(publicKeys))
	for index, publicKey := range publicKeys {
		if problems := validation.IsDNS1123Label(publicKey.Name); len(problems) != 0 {
			return fmt.Errorf("spec.publicKeys[%d].name %q is invalid: %s", index, publicKey.Name, strings.Join(problems, "; "))
		}
		if _, duplicate := names[publicKey.Name]; duplicate {
			return fmt.Errorf("spec.publicKeys[%d].name %q is a duplicate name", index, publicKey.Name)
		}
		names[publicKey.Name] = struct{}{}

		parsed, _, _, rest, err := ssh.ParseAuthorizedKey([]byte(publicKey.Key))
		if err != nil {
			return fmt.Errorf("spec.publicKeys[%d].key is not a valid OpenSSH public key: %w", index, err)
		}
		if len(bytes.TrimSpace(rest)) != 0 {
			return fmt.Errorf("spec.publicKeys[%d].key must contain exactly one OpenSSH public key", index)
		}
		encoded := string(parsed.Marshal())
		if _, duplicate := keys[encoded]; duplicate {
			return fmt.Errorf("spec.publicKeys[%d].key is a duplicate public key", index)
		}
		keys[encoded] = struct{}{}
	}
	return nil
}
