package client

import (
	"testing"

	"golang.org/x/crypto/ssh"
)

func TestServiceClientConfigValidation(t *testing.T) {
	tests := []struct {
		name   string
		config ServiceClientConfig
	}{
		{name: "token", config: ServiceClientConfig{HostKey: ssh.InsecureIgnoreHostKey()}},
		{name: "host key", config: ServiceClientConfig{Token: "token"}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			test.config.setDefaults()
			if err := test.config.validate(); err == nil {
				t.Fatal("validate() returned nil")
			}
		})
	}
}
