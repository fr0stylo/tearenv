// Package client implements tearenv enrollment and service connections.
package client

import (
	"context"
	"errors"
	"fmt"
	"net"
	"time"

	"github.com/fr0stylo/tearenv/internal/protocol"
	"golang.org/x/crypto/ssh"
)

const (
	DefaultServerAddress = "127.0.0.1:2222"
	DefaultIdentity      = "tunnel"
	DefaultDialTimeout   = 10 * time.Second
)

type EnrollmentConfig struct {
	ServerAddress string
	Identity      string
	Invite        string
	HostKey       ssh.HostKeyCallback
	DialTimeout   time.Duration
}

// Enroll redeems a one-time invite and returns a new personal token.
func Enroll(ctx context.Context, config EnrollmentConfig) (string, error) {
	if config.ServerAddress == "" {
		config.ServerAddress = DefaultServerAddress
	}
	if config.Identity == "" {
		return "", errors.New("identity is required")
	}
	if config.Invite == "" {
		return "", errors.New("invite is required")
	}
	if config.HostKey == nil {
		return "", errors.New("host key verifier is required")
	}
	if config.DialTimeout == 0 {
		config.DialTimeout = DefaultDialTimeout
	}

	sshConfig := &ssh.ClientConfig{
		User:            protocol.EnrollmentUser(config.Identity),
		Auth:            []ssh.AuthMethod{ssh.Password(config.Invite)},
		HostKeyCallback: config.HostKey,
		Timeout:         config.DialTimeout,
	}
	connection, err := dialSSH(ctx, config.ServerAddress, sshConfig, config.DialTimeout)
	if err != nil {
		return "", fmt.Errorf("connect to SSH server for enrollment: %w", err)
	}
	defer connection.Close()

	ok, response, err := connection.SendRequest(protocol.EnrollRequestType, true, nil)
	if err != nil {
		return "", fmt.Errorf("request enrollment: %w", err)
	}
	if !ok {
		return "", errors.New("enrollment was rejected")
	}
	var result protocol.EnrollResponse
	if err := ssh.Unmarshal(response, &result); err != nil {
		return "", fmt.Errorf("decode enrollment response: %w", err)
	}
	if result.Token == "" {
		return "", errors.New("server returned an empty token")
	}
	return result.Token, nil
}

func dialSSH(ctx context.Context, address string, config *ssh.ClientConfig, timeout time.Duration) (*ssh.Client, error) {
	dialer := net.Dialer{Timeout: timeout}
	raw, err := dialer.DialContext(ctx, "tcp", address)
	if err != nil {
		return nil, err
	}
	if err := raw.SetDeadline(time.Now().Add(timeout)); err != nil {
		_ = raw.Close()
		return nil, err
	}
	connection, channels, requests, err := ssh.NewClientConn(raw, address, config)
	if err != nil {
		_ = raw.Close()
		return nil, err
	}
	if err := raw.SetDeadline(time.Time{}); err != nil {
		_ = connection.Close()
		return nil, err
	}
	return ssh.NewClient(connection, channels, requests), nil
}
