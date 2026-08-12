package client

import (
	"bytes"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"

	v1alpha1 "github.com/fr0stylo/tearenv/api/v1alpha1"
	"golang.org/x/crypto/ssh"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestSubmitUserRegistrationUsesResourceAPIPath(t *testing.T) {
	t.Parallel()

	want := testUserRegistration(t)
	httpClient := &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		if request.Method != http.MethodPut {
			t.Errorf("method = %s, want PUT", request.Method)
		}
		wantPath := "/apis/tearenv.io/v1alpha1/namespaces/default/userregistrations/alice"
		if request.URL.Path != wantPath {
			t.Errorf("path = %q, want %q", request.URL.Path, wantPath)
		}
		if request.Header.Get("Content-Type") != "application/json" {
			t.Errorf("Content-Type = %q, want application/json", request.Header.Get("Content-Type"))
		}
		var submitted v1alpha1.UserRegistration
		if err := json.NewDecoder(request.Body).Decode(&submitted); err != nil {
			t.Fatalf("decode submitted registration: %v", err)
		}
		if submitted.Spec.Identity != want.Spec.Identity {
			t.Errorf("identity = %q, want %q", submitted.Spec.Identity, want.Spec.Identity)
		}
		submitted.Status = acceptedStatus()
		return registrationHTTPResponse(t, http.StatusCreated, submitted), nil
	})}

	got, err := SubmitUserRegistration(t.Context(), httpClient, "https://api.example.com", want)
	if err != nil {
		t.Fatal(err)
	}
	if !got.Accepted() {
		t.Fatalf("Accepted() = false, status = %#v", got.Status)
	}
}

func TestSubmitUserRegistrationRequiresAcceptance(t *testing.T) {
	t.Parallel()

	httpClient := &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		var registration v1alpha1.UserRegistration
		if err := json.NewDecoder(request.Body).Decode(&registration); err != nil {
			t.Fatalf("decode registration: %v", err)
		}
		registration.Status = &v1alpha1.UserRegistrationStatus{
			Conditions: []metav1.Condition{{
				Type:               v1alpha1.ConditionAccepted,
				Status:             metav1.ConditionUnknown,
				Reason:             "PendingApproval",
				LastTransitionTime: metav1.Now(),
			}},
		}
		return registrationHTTPResponse(t, http.StatusAccepted, registration), nil
	})}

	observed, err := SubmitUserRegistration(t.Context(), httpClient, "https://api.example.com", testUserRegistration(t))
	if err == nil || !strings.Contains(err.Error(), "pending approval") {
		t.Fatalf("SubmitUserRegistration() error = %v, want pending approval", err)
	}
	if observed.Status == nil {
		t.Fatal("pending registration response was discarded")
	}
}

func TestSubmitUserRegistrationReportsRejection(t *testing.T) {
	t.Parallel()

	httpClient := &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		var registration v1alpha1.UserRegistration
		if err := json.NewDecoder(request.Body).Decode(&registration); err != nil {
			t.Fatalf("decode registration: %v", err)
		}
		registration.Status = &v1alpha1.UserRegistrationStatus{
			Conditions: []metav1.Condition{{
				Type:               v1alpha1.ConditionAccepted,
				Status:             metav1.ConditionFalse,
				Reason:             "IdentityAlreadyClaimed",
				Message:            "identity belongs to another account",
				LastTransitionTime: metav1.Now(),
			}},
		}
		return registrationHTTPResponse(t, http.StatusOK, registration), nil
	})}

	observed, err := SubmitUserRegistration(t.Context(), httpClient, "https://api.example.com", testUserRegistration(t))
	if err == nil || !strings.Contains(err.Error(), "rejected") || !strings.Contains(err.Error(), "another account") {
		t.Fatalf("SubmitUserRegistration() error = %v, want rejection reason", err)
	}
	if observed.Status == nil {
		t.Fatal("rejected registration response was discarded")
	}
}

func TestSubmitUserRegistrationReportsAPIError(t *testing.T) {
	t.Parallel()

	httpClient := &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusConflict,
			Status:     "409 Conflict",
			Header:     make(http.Header),
			Body:       io.NopCloser(strings.NewReader("identity is already claimed\n")),
		}, nil
	})}

	_, err := SubmitUserRegistration(t.Context(), httpClient, "https://api.example.com", testUserRegistration(t))
	if err == nil || !strings.Contains(err.Error(), "409 Conflict") || !strings.Contains(err.Error(), "already claimed") {
		t.Fatalf("SubmitUserRegistration() error = %v, want API conflict", err)
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (function roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return function(request)
}

func registrationHTTPResponse(t *testing.T, status int, registration v1alpha1.UserRegistration) *http.Response {
	t.Helper()
	contents, err := json.Marshal(registration)
	if err != nil {
		t.Fatal(err)
	}
	return &http.Response{
		StatusCode: status,
		Status:     http.StatusText(status),
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body:       io.NopCloser(bytes.NewReader(contents)),
	}
}

func testUserRegistration(t *testing.T) v1alpha1.UserRegistration {
	t.Helper()
	registration := v1alpha1.UserRegistration{
		TypeMeta: metav1.TypeMeta{APIVersion: v1alpha1.APIVersion, Kind: v1alpha1.UserRegistrationKind},
		ObjectMeta: metav1.ObjectMeta{
			Name:      "alice",
			Namespace: "default",
		},
		Spec: v1alpha1.UserRegistrationSpec{
			Identity: "alice",
			PublicKeys: []v1alpha1.SSHPublicKey{{
				Name: "laptop",
				Key:  strings.TrimSpace(string(ssh.MarshalAuthorizedKey(registrationPublicKey(t)))),
			}},
		},
	}
	return registration
}

func registrationPublicKey(t *testing.T) ssh.PublicKey {
	t.Helper()
	publicKey, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	key, err := ssh.NewPublicKey(publicKey)
	if err != nil {
		t.Fatal(err)
	}
	return key
}

func acceptedStatus() *v1alpha1.UserRegistrationStatus {
	return &v1alpha1.UserRegistrationStatus{
		ObservedGeneration: 1,
		Conditions: []metav1.Condition{{
			Type:               v1alpha1.ConditionAccepted,
			Status:             metav1.ConditionTrue,
			Reason:             "Approved",
			LastTransitionTime: metav1.Now(),
		}},
	}
}
