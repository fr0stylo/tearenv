package registration

import (
	"crypto/sha256"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"

	v1alpha1 "github.com/fr0stylo/tearenv/api/v1alpha1"
)

const requestBodyLimit = 1 << 20

// NewHandler serves health checks and the namespaced UserRegistration resource
// endpoints. When bearerToken is nonempty, resource requests must authenticate.
func NewHandler(store *Store, bearerToken string) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", health)
	mux.HandleFunc("GET /readyz", health)
	path := "/apis/" + v1alpha1.APIVersion + "/namespaces/{namespace}/" + v1alpha1.UserRegistrationResource + "/{name}"
	mux.HandleFunc("PUT "+path, func(response http.ResponseWriter, request *http.Request) {
		if !authorize(response, request, bearerToken) {
			return
		}
		putRegistration(response, request, store)
	})
	mux.HandleFunc("GET "+path, func(response http.ResponseWriter, request *http.Request) {
		if !authorize(response, request, bearerToken) {
			return
		}
		getRegistration(response, request, store)
	})
	return mux
}

func health(response http.ResponseWriter, _ *http.Request) {
	response.Header().Set("Content-Type", "text/plain; charset=utf-8")
	response.WriteHeader(http.StatusOK)
	_, _ = response.Write([]byte("ok\n"))
}

func authorize(response http.ResponseWriter, request *http.Request, bearerToken string) bool {
	if bearerToken == "" {
		return true
	}
	provided, found := strings.CutPrefix(request.Header.Get("Authorization"), "Bearer ")
	wantHash := sha256.Sum256([]byte(bearerToken))
	providedHash := sha256.Sum256([]byte(provided))
	if !found || subtle.ConstantTimeCompare(wantHash[:], providedHash[:]) != 1 {
		response.Header().Set("WWW-Authenticate", `Bearer realm="tearenv-registration"`)
		http.Error(response, "authentication required", http.StatusUnauthorized)
		return false
	}
	return true
}

func putRegistration(response http.ResponseWriter, request *http.Request, store *Store) {
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
	stored, created, err := store.Put(registration)
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

func getRegistration(response http.ResponseWriter, request *http.Request, store *Store) {
	registration, err := store.Get(request.PathValue("namespace"), request.PathValue("name"))
	if errors.Is(err, ErrNotFound) {
		http.Error(response, err.Error(), http.StatusNotFound)
		return
	}
	if err != nil {
		http.Error(response, fmt.Sprintf("load user registration: %v", err), http.StatusInternalServerError)
		return
	}
	writeRegistration(response, http.StatusOK, registration)
}

func writeRegistration(response http.ResponseWriter, status int, registration v1alpha1.UserRegistration) {
	contents, err := json.Marshal(registration)
	if err != nil {
		http.Error(response, fmt.Sprintf("encode user registration: %v", err), http.StatusInternalServerError)
		return
	}
	response.Header().Set("Content-Type", "application/json")
	response.WriteHeader(status)
	_, _ = response.Write(contents)
}
