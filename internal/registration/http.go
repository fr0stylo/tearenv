package registration

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"

	v1alpha1 "github.com/fr0stylo/tearenv/api/v1alpha1"
)

const requestBodyLimit = 1 << 20

// NewHandler serves the namespaced UserRegistration resource endpoints.
func NewHandler(store *Store) http.Handler {
	mux := http.NewServeMux()
	path := "/apis/" + v1alpha1.APIVersion + "/namespaces/{namespace}/" + v1alpha1.UserRegistrationResource + "/{name}"
	mux.HandleFunc("PUT "+path, func(response http.ResponseWriter, request *http.Request) {
		putRegistration(response, request, store)
	})
	mux.HandleFunc("GET "+path, func(response http.ResponseWriter, request *http.Request) {
		getRegistration(response, request, store)
	})
	return mux
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
