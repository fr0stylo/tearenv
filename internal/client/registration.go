package client

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"reflect"
	"strings"

	v1alpha1 "github.com/fr0stylo/tearenv/api/v1alpha1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

const registrationResponseLimit = 1 << 20

type registrationRequestOptions struct {
	bearerToken string
}

// RegistrationRequestOption configures a registration API request.
type RegistrationRequestOption func(*registrationRequestOptions)

// WithRegistrationToken authenticates the request with a bearer enrollment token.
func WithRegistrationToken(token string) RegistrationRequestOption {
	return func(options *registrationRequestOptions) {
		options.bearerToken = strings.TrimSpace(token)
	}
}

// SubmitUserRegistration stores a registration through the tearenv resource
// API and returns the server-owned representation.
func SubmitUserRegistration(
	ctx context.Context,
	httpClient *http.Client,
	baseURL string,
	registration v1alpha1.UserRegistration,
	requestOptions ...RegistrationRequestOption,
) (v1alpha1.UserRegistration, error) {
	if httpClient == nil {
		return v1alpha1.UserRegistration{}, errors.New("HTTP client is required")
	}
	if err := registration.Validate(); err != nil {
		return v1alpha1.UserRegistration{}, fmt.Errorf("validate user registration: %w", err)
	}
	if registration.Namespace == "" {
		return v1alpha1.UserRegistration{}, errors.New("metadata.namespace is required for API submission")
	}
	endpoint, err := userRegistrationURL(baseURL, registration.Namespace, registration.Name)
	if err != nil {
		return v1alpha1.UserRegistration{}, err
	}
	body, err := json.Marshal(registration)
	if err != nil {
		return v1alpha1.UserRegistration{}, fmt.Errorf("encode user registration: %w", err)
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPut, endpoint, bytes.NewReader(body))
	if err != nil {
		return v1alpha1.UserRegistration{}, fmt.Errorf("create user registration request: %w", err)
	}
	request.Header.Set("Accept", "application/json")
	request.Header.Set("Content-Type", "application/json")
	options := registrationRequestOptions{}
	for _, configure := range requestOptions {
		configure(&options)
	}
	if options.bearerToken != "" {
		request.Header.Set("Authorization", "Bearer "+options.bearerToken)
	}
	response, err := httpClient.Do(request)
	if err != nil {
		return v1alpha1.UserRegistration{}, fmt.Errorf("submit user registration: %w", err)
	}
	defer response.Body.Close()
	contents, err := io.ReadAll(io.LimitReader(response.Body, registrationResponseLimit+1))
	if err != nil {
		return v1alpha1.UserRegistration{}, fmt.Errorf("read user registration response: %w", err)
	}
	if len(contents) > registrationResponseLimit {
		return v1alpha1.UserRegistration{}, errors.New("user registration response exceeds 1 MiB")
	}
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		message := strings.TrimSpace(string(contents))
		if message == "" {
			message = response.Status
		}
		return v1alpha1.UserRegistration{}, fmt.Errorf("submit user registration: API returned %s: %s", response.Status, message)
	}
	accepted, err := v1alpha1.LoadUserRegistration(contents)
	if err != nil {
		return v1alpha1.UserRegistration{}, fmt.Errorf("decode user registration response: %w", err)
	}
	if accepted.Namespace != registration.Namespace || accepted.Name != registration.Name || !reflect.DeepEqual(accepted.Spec, registration.Spec) {
		return v1alpha1.UserRegistration{}, errors.New("user registration response does not match the submitted resource")
	}
	condition := accepted.AcceptanceCondition()
	if condition == nil || condition.Status == metav1.ConditionUnknown {
		return accepted, errors.New("user registration is pending approval")
	}
	if condition.Status == metav1.ConditionFalse {
		message := strings.TrimSpace(condition.Message)
		if message == "" {
			message = strings.TrimSpace(condition.Reason)
		}
		if message == "" {
			return accepted, errors.New("user registration was rejected")
		}
		return accepted, fmt.Errorf("user registration was rejected: %s", message)
	}
	return accepted, nil
}

func userRegistrationURL(baseURL, namespace, name string) (string, error) {
	parsed, err := url.Parse(strings.TrimSpace(baseURL))
	if err != nil {
		return "", fmt.Errorf("parse API URL: %w", err)
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return "", errors.New("API URL must use http or https")
	}
	if parsed.Host == "" {
		return "", errors.New("API URL host is required")
	}
	if parsed.RawQuery != "" || parsed.Fragment != "" {
		return "", errors.New("API URL must not contain a query or fragment")
	}
	basePath := strings.TrimRight(parsed.EscapedPath(), "/")
	parsed.RawPath = ""
	parsed.Path = ""
	return parsed.String() + basePath + "/apis/" + v1alpha1.APIVersion + "/namespaces/" +
		url.PathEscape(namespace) + "/" + v1alpha1.UserRegistrationResource + "/" + url.PathEscape(name), nil
}
