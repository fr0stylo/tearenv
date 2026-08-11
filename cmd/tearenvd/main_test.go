package main

import (
	"strings"
	"testing"
)

func TestNewScalerBackendAllowsStaticServices(t *testing.T) {
	backend, err := newScalerBackend("")
	if err != nil {
		t.Fatal(err)
	}
	if backend != nil {
		t.Fatalf("backend = %#v, want nil", backend)
	}
}

func TestNewScalerBackendRejectsUnknownBackend(t *testing.T) {
	_, err := newScalerBackend("docker")
	if err == nil || !strings.Contains(err.Error(), `unsupported scaler backend "docker"`) {
		t.Fatalf("newScalerBackend() error = %v", err)
	}
}
