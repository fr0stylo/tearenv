package client

import (
	"crypto/ed25519"
	"crypto/rand"
	"testing"

	"golang.org/x/crypto/ssh"
)

func TestServiceClientConfigValidation(t *testing.T) {
	_, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	signer, err := ssh.NewSignerFromKey(privateKey)
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name   string
		config ServiceClientConfig
	}{
		{name: "credentials", config: ServiceClientConfig{HostKey: ssh.InsecureIgnoreHostKey()}},
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
	valid := ServiceClientConfig{Signer: signer, HostKey: ssh.InsecureIgnoreHostKey()}
	valid.setDefaults()
	if err := valid.validate(); err != nil {
		t.Fatalf("validate() rejected a private key: %v", err)
	}
}
