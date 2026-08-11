package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"os"
	"os/signal"
	"os/user"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"syscall"

	"github.com/fr0stylo/tearenv/internal/client"
	"github.com/fr0stylo/tearenv/internal/profile"
	"github.com/fr0stylo/tearenv/internal/protocol"
	"github.com/spf13/cobra"
	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/knownhosts"
)

type loginOptions struct {
	serverAddress  string
	identity       string
	invite         string
	profilePath    string
	knownHostsPath string
	insecure       bool
}

type connectOptions struct {
	profilePath    string
	listenHost     string
	serverAddress  string
	identity       string
	token          string
	knownHostsPath string
	insecure       bool
}

type servicesOptions struct {
	profilePath string
}

func main() {
	if err := run(os.Args[1:]); err != nil {
		slog.Error("tearenv stopped", "error", err)
		os.Exit(1)
	}
}

func run(arguments []string) error {
	command := newRootCommand()
	command.SetArgs(arguments)
	return command.Execute()
}

func newRootCommand() *cobra.Command {
	command := &cobra.Command{
		Use:           "tearenv",
		Short:         "Expose identity-authorized remote services on localhost",
		SilenceErrors: true,
	}
	command.AddCommand(
		newLoginCommand(),
		newServicesCommand(),
		newConnectCommand(),
	)
	return command
}

func newLoginCommand() *cobra.Command {
	options := loginOptions{
		serverAddress:  client.DefaultServerAddress,
		identity:       defaultIdentity(),
		invite:         os.Getenv("TEARENV_INVITE"),
		profilePath:    defaultProfilePath(),
		knownHostsPath: defaultKnownHostsPath(),
	}
	command := &cobra.Command{
		Use:   "login",
		Short: "Redeem a one-time invite and save a local profile",
		Args:  cobra.NoArgs,
		RunE: func(command *cobra.Command, _ []string) error {
			command.SilenceUsage = true
			return login(command.Context(), options)
		},
	}
	flags := command.Flags()
	flags.StringVar(&options.serverAddress, "server", options.serverAddress, "SSH tunnel server")
	flags.StringVar(&options.identity, "identity", options.identity, "developer identity from the invite")
	flags.StringVar(&options.invite, "invite", options.invite, "one-time invite (or TEARENV_INVITE)")
	flags.StringVar(&options.profilePath, "config", options.profilePath, "local profile destination")
	flags.StringVar(&options.knownHostsPath, "known-hosts", options.knownHostsPath, "SSH known_hosts file")
	flags.BoolVar(&options.insecure, "insecure-skip-host-key-check", false, "disable host identity verification (development only)")
	return command
}

func login(ctx context.Context, options loginOptions) error {
	hostKey, err := hostKeyCallback(options.knownHostsPath, options.insecure)
	if err != nil {
		return err
	}
	ctx, stop := signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM)
	defer stop()
	token, err := client.Enroll(ctx, client.EnrollmentConfig{
		ServerAddress: options.serverAddress,
		Identity:      options.identity,
		Invite:        options.invite,
		HostKey:       hostKey,
	})
	if err != nil {
		return err
	}
	if err := profile.Save(options.profilePath, profile.Profile{
		ServerAddress: options.serverAddress,
		Identity:      options.identity,
		Token:         token,
		KnownHosts:    options.knownHostsPath,
		Insecure:      options.insecure,
	}); err != nil {
		return fmt.Errorf("save login: %w", err)
	}
	slog.Info("login saved", "identity", options.identity, "server", options.serverAddress, "config", options.profilePath)
	return nil
}

func newServicesCommand() *cobra.Command {
	options := servicesOptions{profilePath: defaultProfilePath()}
	command := &cobra.Command{
		Use:   "services",
		Short: "List services granted to the saved identity",
		Args:  cobra.NoArgs,
		RunE: func(command *cobra.Command, _ []string) error {
			command.SilenceUsage = true
			return services(command.Context(), options, command.OutOrStdout())
		},
	}
	command.Flags().StringVar(&options.profilePath, "config", options.profilePath, "local profile created by tearenv login")
	return command
}

func services(ctx context.Context, options servicesOptions, output io.Writer) error {
	saved, err := profile.Load(options.profilePath)
	if err != nil {
		return fmt.Errorf("load login; run 'tearenv login' first: %w", err)
	}
	hostKey, err := hostKeyCallback(saved.KnownHosts, saved.Insecure)
	if err != nil {
		return err
	}
	catalog, err := client.ListServices(ctx, client.ServiceClientConfig{
		ServerAddress: saved.ServerAddress,
		Identity:      saved.Identity,
		Token:         saved.Token,
		HostKey:       hostKey,
	})
	if err != nil {
		return err
	}
	for _, service := range catalog {
		fmt.Fprintf(output, "%s\t127.0.0.1:%d\n", service.Name, service.LocalPort)
	}
	return nil
}

func newConnectCommand() *cobra.Command {
	options := connectOptions{
		profilePath: defaultProfilePath(),
		listenHost:  "127.0.0.1",
		token:       os.Getenv("TEARENV_TOKEN"),
	}
	command := &cobra.Command{
		Use:   "connect [service[=host:port] ...]",
		Short: "Expose granted services on local TCP listeners",
		Args:  cobra.ArbitraryArgs,
		RunE: func(command *cobra.Command, specifications []string) error {
			command.SilenceUsage = true
			return connect(command.Context(), options, specifications)
		},
	}
	flags := command.Flags()
	flags.StringVar(&options.profilePath, "config", options.profilePath, "local profile created by tearenv login")
	flags.StringVar(&options.listenHost, "listen-host", options.listenHost, "default local listen host")
	flags.StringVar(&options.serverAddress, "server", "", "override the saved SSH server")
	flags.StringVar(&options.identity, "identity", "", "override the saved identity")
	flags.StringVar(&options.token, "token", options.token, "override the saved token (or TEARENV_TOKEN)")
	flags.StringVar(&options.knownHostsPath, "known-hosts", "", "override the saved known_hosts file")
	flags.BoolVar(&options.insecure, "insecure-skip-host-key-check", false, "disable host identity verification (development only)")
	return command
}

func connect(ctx context.Context, options connectOptions, specifications []string) error {
	saved, err := profile.Load(options.profilePath)
	if err != nil {
		return fmt.Errorf("load login; run 'tearenv login' first: %w", err)
	}
	if options.serverAddress != "" {
		saved.ServerAddress = options.serverAddress
	}
	if options.identity != "" {
		saved.Identity = options.identity
	}
	if options.token != "" {
		saved.Token = options.token
	}
	if options.knownHostsPath != "" {
		saved.KnownHosts = options.knownHostsPath
	}
	if options.insecure {
		saved.Insecure = true
	}
	hostKey, err := hostKeyCallback(saved.KnownHosts, saved.Insecure)
	if err != nil {
		return err
	}
	clientConfig := client.ServiceClientConfig{
		ServerAddress: saved.ServerAddress,
		Identity:      saved.Identity,
		Token:         saved.Token,
		HostKey:       hostKey,
	}
	ctx, stop := signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM)
	defer stop()
	catalog, err := client.ListServices(ctx, clientConfig)
	if err != nil {
		return err
	}
	requested, err := selectServices(catalog, specifications, options.listenHost)
	if err != nil {
		return err
	}
	return client.RunServices(ctx, clientConfig, requested, nil)
}

func selectServices(catalog []protocol.Service, specifications []string, listenHost string) ([]client.LocalService, error) {
	available := make(map[string]protocol.Service, len(catalog))
	for _, service := range catalog {
		available[service.Name] = service
	}
	if len(specifications) == 0 {
		specifications = make([]string, 0, len(catalog))
		for name := range available {
			specifications = append(specifications, name)
		}
		sort.Strings(specifications)
	}
	selected := make([]client.LocalService, 0, len(specifications))
	for _, specification := range specifications {
		name, listenAddress, overridden := strings.Cut(specification, "=")
		service, exists := available[name]
		if !exists {
			return nil, fmt.Errorf("service %q is not granted to this identity", name)
		}
		if !overridden {
			listenAddress = net.JoinHostPort(listenHost, strconv.Itoa(int(service.LocalPort)))
		}
		selected = append(selected, client.LocalService{Name: name, ListenAddress: listenAddress})
	}
	if len(selected) == 0 {
		return nil, errors.New("no services are granted to this identity")
	}
	return selected, nil
}

func defaultIdentity() string {
	current, err := user.Current()
	if err != nil {
		return client.DefaultIdentity
	}
	return current.Username
}

func hostKeyCallback(path string, insecure bool) (ssh.HostKeyCallback, error) {
	if insecure {
		slog.Warn("SSH host key verification is disabled")
		return ssh.InsecureIgnoreHostKey(), nil //nolint:gosec -- explicit development flag
	}
	if path == "" {
		return nil, errors.New("known-hosts path is required")
	}
	callback, err := knownhosts.New(path)
	if err != nil {
		return nil, fmt.Errorf("load known_hosts: %w", err)
	}
	return callback, nil
}

func defaultKnownHostsPath() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".ssh", "known_hosts")
}

func defaultProfilePath() string {
	directory, err := os.UserConfigDir()
	if err != nil {
		return ".tearenv.json"
	}
	return filepath.Join(directory, "tearenv", "config.json")
}
