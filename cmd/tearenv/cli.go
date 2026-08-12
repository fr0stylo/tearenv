package main

import (
	"net/http"
	"time"

	"github.com/fr0stylo/tearenv/internal/client"
	"github.com/spf13/cobra"
)

const defaultAPIURL = "http://127.0.0.1:8080"

type loginOptions struct {
	serverAddress    string
	apiURL           string
	namespace        string
	identity         string
	identityDefault  string
	privateKeyPath   string
	registrationPath string
	profilePath      string
	knownHostsPath   string
	insecure         bool
	httpClient       *http.Client
}

type connectOptions struct {
	profilePath    string
	listenHost     string
	serverAddress  string
	identity       string
	privateKeyPath string
	knownHostsPath string
	insecure       bool
}

type servicesOptions struct {
	profilePath string
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
		SilenceUsage:  true,
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
		serverAddress:    client.DefaultServerAddress,
		apiURL:           defaultAPIURL,
		namespace:        "default",
		identityDefault:  defaultIdentity(),
		profilePath:      defaultProfilePath(),
		knownHostsPath:   defaultKnownHostsPath(),
		privateKeyPath:   defaultPrivateKeyPath(),
		registrationPath: defaultRegistrationPath(),
		httpClient:       &http.Client{Timeout: 15 * time.Second},
	}
	command := &cobra.Command{
		Use:   "login",
		Short: "Create a local key and user registration",
		Args:  cobra.NoArgs,
		RunE: func(command *cobra.Command, _ []string) error {
			return login(command.Context(), options, command.InOrStdin(), command.OutOrStdout(), command.ErrOrStderr())
		},
	}
	flags := command.Flags()
	flags.StringVar(&options.apiURL, "api-url", options.apiURL, "tearenv registration API base URL")
	flags.StringVar(&options.namespace, "namespace", options.namespace, "registration API namespace")
	flags.StringVar(&options.serverAddress, "server", options.serverAddress, "SSH tunnel server")
	flags.StringVar(&options.identity, "identity", "", "tearenv identity; prompts with the hostname by default")
	flags.StringVar(&options.privateKeyPath, "private-key", options.privateKeyPath, "local SSH private key")
	flags.StringVar(&options.registrationPath, "registration", options.registrationPath, "UserRegistration YAML destination")
	flags.StringVar(&options.profilePath, "config", options.profilePath, "local profile destination")
	flags.StringVar(&options.knownHostsPath, "known-hosts", options.knownHostsPath, "SSH known_hosts file")
	flags.BoolVar(&options.insecure, "insecure-skip-host-key-check", false, "disable host identity verification (development only)")
	return command
}

func newServicesCommand() *cobra.Command {
	options := servicesOptions{profilePath: defaultProfilePath()}
	command := &cobra.Command{
		Use:   "services",
		Short: "List services granted to the saved identity",
		Args:  cobra.NoArgs,
		RunE: func(command *cobra.Command, _ []string) error {
			return services(command.Context(), options, command.OutOrStdout())
		},
	}
	command.Flags().StringVar(&options.profilePath, "config", options.profilePath, "local profile created by tearenv login")
	return command
}

func newConnectCommand() *cobra.Command {
	options := connectOptions{
		profilePath: defaultProfilePath(),
		listenHost:  "127.0.0.1",
	}
	command := &cobra.Command{
		Use:   "connect [service[=host:port] ...]",
		Short: "Expose granted services on local TCP listeners",
		Args:  cobra.ArbitraryArgs,
		RunE: func(command *cobra.Command, specifications []string) error {
			return connect(command.Context(), options, specifications)
		},
	}
	flags := command.Flags()
	flags.StringVar(&options.profilePath, "config", options.profilePath, "local profile created by tearenv login")
	flags.StringVar(&options.listenHost, "listen-host", options.listenHost, "default local listen host")
	flags.StringVar(&options.serverAddress, "server", "", "override the saved SSH server")
	flags.StringVar(&options.identity, "identity", "", "override the saved identity")
	flags.StringVar(&options.privateKeyPath, "private-key", "", "override the saved SSH private key")
	flags.StringVar(&options.knownHostsPath, "known-hosts", "", "override the saved known_hosts file")
	flags.BoolVar(&options.insecure, "insecure-skip-host-key-check", false, "disable host identity verification (development only)")
	return command
}
