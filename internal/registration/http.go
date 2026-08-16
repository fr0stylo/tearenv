package registration

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"

	v1alpha1 "github.com/fr0stylo/tearenv/api/v1alpha1"
	"github.com/fr0stylo/tearenv/internal/authn"
)

const requestBodyLimit = 1 << 20

// HandlerOptions configures registration API authentication and discovery.
type HandlerOptions struct {
	Authenticator authn.Authenticator
	Configuration authn.Configuration
	TokenExchange http.Handler
}

// NewHandler preserves the static-token API used by existing embedders.
func NewHandler(store *Store, bearerToken string) http.Handler {
	var authenticator authn.Authenticator = authn.Anonymous{}
	mode := authn.MethodAnonymous
	if strings.TrimSpace(bearerToken) != "" {
		authenticator, _ = authn.NewStaticToken(bearerToken)
		mode = authn.MethodToken
	}
	return NewHandlerWithOptions(store, HandlerOptions{
		Authenticator: authenticator,
		Configuration: authn.Configuration{Mode: mode},
	})
}

// NewHandlerWithOptions serves health, discovery, registration, and token exchange endpoints.
func NewHandlerWithOptions(store *Store, options HandlerOptions) http.Handler {
	if options.Authenticator == nil {
		options.Authenticator = authn.Anonymous{}
	}
	options.Configuration = options.Configuration.Normalized()
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", health)
	mux.HandleFunc("GET /readyz", health)
	mux.HandleFunc("GET /.well-known/tearenv-configuration", func(response http.ResponseWriter, _ *http.Request) {
		writeJSON(response, http.StatusOK, options.Configuration)
	})
	if options.TokenExchange != nil {
		mux.Handle("POST /oauth/token", options.TokenExchange)
	}
	path := "/apis/" + v1alpha1.APIVersion + "/namespaces/{namespace}/" + v1alpha1.UserRegistrationResource + "/{name}"
	mux.HandleFunc("PUT "+path, func(response http.ResponseWriter, request *http.Request) {
		principal, authenticated := authorize(response, request, options.Authenticator)
		if !authenticated {
			return
		}
		putRegistration(response, request, store, principal)
	})
	mux.HandleFunc("GET "+path, func(response http.ResponseWriter, request *http.Request) {
		principal, authenticated := authorize(response, request, options.Authenticator)
		if !authenticated {
			return
		}
		getRegistration(response, request, store, principal)
	})
	return mux
}

func health(response http.ResponseWriter, _ *http.Request) {
	response.Header().Set("Content-Type", "text/plain; charset=utf-8")
	response.WriteHeader(http.StatusOK)
	_, _ = response.Write([]byte("ok\n"))
}

func authorize(response http.ResponseWriter, request *http.Request, authenticator authn.Authenticator) (authn.Principal, bool) {
	bearerToken := ""
	if _, anonymous := authenticator.(authn.Anonymous); !anonymous {
		var err error
		bearerToken, err = authn.Bearer(request.Header.Get("Authorization"))
		if err != nil {
			writeAuthenticationError(response)
			return authn.Principal{}, false
		}
	}
	principal, err := authenticator.Authenticate(request.Context(), bearerToken)
	if err != nil {
		writeAuthenticationError(response)
		return authn.Principal{}, false
	}
	return principal, true
}

func writeAuthenticationError(response http.ResponseWriter) {
	response.Header().Set("WWW-Authenticate", `Bearer realm="tearenv-registration", error="invalid_token"`)
	http.Error(response, "authentication required", http.StatusUnauthorized)
}

func putRegistration(response http.ResponseWriter, request *http.Request, store *Store, principal authn.Principal) {
	mediaType := strings.TrimSpace(strings.Split(request.Header.Get("Content-Type"), ";")[0])
	if mediaType != "application/json" {
		http.Error(response, "Content-Type must be application/json", http.StatusUnsupportedMediaType)
		return
	}
	contents, err := io.ReadAll(io.LimitReader(request.Body, requestBodyLimit+1))
	if err != nil {
		http.Error(response, "read request body", http.StatusBadRequest)
		return
	}
	if len(contents) > requestBodyLimit {
		http.Error(response, "request body exceeds 1 MiB", http.StatusRequestEntityTooLarge)
		return
	}
	registration, err := v1alpha1.LoadUserRegistration(contents)
	if err != nil {
		http.Error(response, err.Error(), http.StatusBadRequest)
		return
	}
	if registration.Namespace != request.PathValue("namespace") || registration.Name != request.PathValue("name") {
		http.Error(response, "request path must match metadata.namespace and metadata.name", http.StatusBadRequest)
		return
	}
	stored, created, err := store.PutAs(registration, principal)
	if errors.Is(err, ErrForbidden) {
		http.Error(response, err.Error(), http.StatusForbidden)
		return
	}
	if errors.Is(err, ErrConflict) {
		http.Error(response, err.Error(), http.StatusConflict)
		return
	}
	if err != nil {
		http.Error(response, fmt.Sprintf("store user registration: %v", err), http.StatusInternalServerError)
		return
	}
	status := http.StatusOK
	if created {
		status = http.StatusCreated
	}
	writeRegistration(response, status, stored)
}

func getRegistration(response http.ResponseWriter, request *http.Request, store *Store, principal authn.Principal) {
	registration, err := store.GetAs(request.PathValue("namespace"), request.PathValue("name"), principal)
	if errors.Is(err, ErrNotFound) {
		http.Error(response, err.Error(), http.StatusNotFound)
		return
	}
	if errors.Is(err, ErrForbidden) {
		http.Error(response, err.Error(), http.StatusForbidden)
		return
	}
	if err != nil {
		http.Error(response, fmt.Sprintf("load user registration: %v", err), http.StatusInternalServerError)
		return
	}
	writeRegistration(response, http.StatusOK, registration)
}

func writeRegistration(response http.ResponseWriter, status int, registration v1alpha1.UserRegistration) {
	writeJSON(response, status, registration)
}

func writeJSON(response http.ResponseWriter, status int, value any) {
	contents, err := json.Marshal(value)
	if err != nil {
		http.Error(response, fmt.Sprintf("encode user registration: %v", err), http.StatusInternalServerError)
		return
	}
	response.Header().Set("Content-Type", "application/json")
	response.WriteHeader(status)
	_, _ = response.Write(contents)
}
