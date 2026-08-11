package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
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
	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/knownhosts"
)

func main() {
	if err := run(os.Args[1:]); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return
		}
		slog.Error("tearenv stopped", "error", err)
		os.Exit(1)
	}
}

func run(arguments []string) error {
	if len(arguments) == 0 {
		printUsage()
		return errors.New("a command is required")
	}
	switch arguments[0] {
	case "login":
		return login(arguments[1:])
	case "connect":
		return connect(arguments[1:])
	case "services":
		return services(arguments[1:])
	case "help", "-h", "--help":
		printUsage()
		return nil
	default:
		printUsage()
		return fmt.Errorf("unknown command %q", arguments[0])
	}
}

func login(arguments []string) error {
	flags := flag.NewFlagSet("tearenv login", flag.ContinueOnError)
	serverAddress := flags.String("server", client.DefaultServerAddress, "SSH tunnel server")
	identity := flags.String("identity", defaultIdentity(), "developer identity from the invite")
	invite := flags.String("invite", os.Getenv("TEARENV_INVITE"), "one-time invite (or TEARENV_INVITE)")
	profilePath := flags.String("config", defaultProfilePath(), "local profile destination")
	knownHostsPath := flags.String("known-hosts", defaultKnownHostsPath(), "SSH known_hosts file")
	insecure := flags.Bool("insecure-skip-host-key-check", false, "disable host identity verification (development only)")
	if err := flags.Parse(arguments); err != nil {
		return err
	}
	hostKey, err := hostKeyCallback(*knownHostsPath, *insecure)
	if err != nil {
		return err
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	token, err := client.Enroll(ctx, client.EnrollmentConfig{
		ServerAddress: *serverAddress,
		Identity:      *identity,
		Invite:        *invite,
		HostKey:       hostKey,
	})
	if err != nil {
		return err
	}
	if err := profile.Save(*profilePath, profile.Profile{
		ServerAddress: *serverAddress,
		Identity:      *identity,
		Token:         token,
		KnownHosts:    *knownHostsPath,
		Insecure:      *insecure,
	}); err != nil {
		return fmt.Errorf("save login: %w", err)
	}
	slog.Info("login saved", "identity", *identity, "server", *serverAddress, "config", *profilePath)
	return nil
}

func connect(arguments []string) error {
	flags := flag.NewFlagSet("tearenv connect", flag.ContinueOnError)
	profilePath := flags.String("config", defaultProfilePath(), "local profile created by tearenv login")
	listenHost := flags.String("listen-host", "127.0.0.1", "default local listen host")
	serverAddress := flags.String("server", "", "override the saved SSH server")
	identity := flags.String("identity", "", "override the saved identity")
	token := flags.String("token", os.Getenv("TEARENV_TOKEN"), "override the saved token (or TEARENV_TOKEN)")
	knownHostsPath := flags.String("known-hosts", "", "override the saved known_hosts file")
	insecure := flags.Bool("insecure-skip-host-key-check", false, "disable host identity verification (development only)")
	if err := flags.Parse(arguments); err != nil {
		return err
	}
	saved, err := profile.Load(*profilePath)
	if err != nil {
		return fmt.Errorf("load login; run 'tearenv login' first: %w", err)
	}
	if *serverAddress != "" {
		saved.ServerAddress = *serverAddress
	}
	if *identity != "" {
		saved.Identity = *identity
	}
	if *token != "" {
		saved.Token = *token
	}
	if *knownHostsPath != "" {
		saved.KnownHosts = *knownHostsPath
	}
	if *insecure {
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
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	catalog, err := client.ListServices(ctx, clientConfig)
	if err != nil {
		return err
	}
	requested, err := selectServices(catalog, flags.Args(), *listenHost)
	if err != nil {
		return err
	}
	return client.RunServices(ctx, clientConfig, requested, nil)
}

func services(arguments []string) error {
	flags := flag.NewFlagSet("tearenv services", flag.ContinueOnError)
	profilePath := flags.String("config", defaultProfilePath(), "local profile created by tearenv login")
	if err := flags.Parse(arguments); err != nil {
		return err
	}
	saved, err := profile.Load(*profilePath)
	if err != nil {
		return fmt.Errorf("load login; run 'tearenv login' first: %w", err)
	}
	hostKey, err := hostKeyCallback(saved.KnownHosts, saved.Insecure)
	if err != nil {
		return err
	}
	catalog, err := client.ListServices(context.Background(), client.ServiceClientConfig{
		ServerAddress: saved.ServerAddress,
		Identity:      saved.Identity,
		Token:         saved.Token,
		HostKey:       hostKey,
	})
	if err != nil {
		return err
	}
	for _, service := range catalog {
		fmt.Fprintf(os.Stdout, "%s\t127.0.0.1:%d\n", service.Name, service.LocalPort)
	}
	return nil
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

func printUsage() {
	fmt.Fprintln(os.Stderr, "usage: tearenv <login|services|connect> [options]")
	fmt.Fprintln(os.Stderr, "  login    redeem a one-time invite and save a local profile")
	fmt.Fprintln(os.Stderr, "  services list services granted to the saved identity")
	fmt.Fprintln(os.Stderr, "  connect  expose granted services on local TCP listeners")
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
