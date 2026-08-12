package main

import (
	"os"

	"github.com/fr0stylo/tearenv/internal/client"
	"github.com/spf13/cobra"
)

type loginOptions struct {
	method         string
	serverAddress  string
	identity       string
	invite         string
	privateKeyPath string
	profilePath    string
	knownHostsPath string
	kubeconfig     string
	kubeContext    string
	kubeNamespace  string
	kubeSecret     string
	insecure       bool
}

type connectOptions struct {
	profilePath    string
	listenHost     string
	serverAddress  string
	identity       string
	token          string
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
		method:         "token",
		serverAddress:  client.DefaultServerAddress,
		identity:       defaultIdentity(),
		invite:         os.Getenv("TEARENV_INVITE"),
		profilePath:    defaultProfilePath(),
		knownHostsPath: defaultKnownHostsPath(),
		privateKeyPath: defaultPrivateKeyPath(),
		kubeNamespace:  "tearenv-system",
		kubeSecret:     "tearenv-authorized-keys",
	}
	command := &cobra.Command{
		Use:   "login",
		Short: "Authenticate and save a local profile",
		Args:  cobra.NoArgs,
		RunE: func(command *cobra.Command, _ []string) error {
			return login(command.Context(), options)
		},
	}
	flags := command.Flags()
	flags.StringVar(&options.method, "method", options.method, "login method (token or kubernetes)")
	flags.StringVar(&options.serverAddress, "server", options.serverAddress, "SSH tunnel server")
	flags.StringVar(&options.identity, "identity", options.identity, "developer identity from the invite")
	flags.StringVar(&options.invite, "invite", options.invite, "one-time invite (or TEARENV_INVITE)")
	flags.StringVar(&options.privateKeyPath, "private-key", options.privateKeyPath, "local SSH private key for Kubernetes login")
	flags.StringVar(&options.profilePath, "config", options.profilePath, "local profile destination")
	flags.StringVar(&options.knownHostsPath, "known-hosts", options.knownHostsPath, "SSH known_hosts file")
	flags.StringVar(&options.kubeconfig, "kubeconfig", "", "kubeconfig used to register the public key")
	flags.StringVar(&options.kubeContext, "kubernetes-context", "", "kubeconfig context used to register the public key")
	flags.StringVar(&options.kubeNamespace, "kubernetes-namespace", options.kubeNamespace, "namespace containing the authorized keys Secret")
	flags.StringVar(&options.kubeSecret, "kubernetes-secret", options.kubeSecret, "authorized keys Secret name")
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
		token:       os.Getenv("TEARENV_TOKEN"),
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
	flags.StringVar(&options.token, "token", options.token, "override the saved token (or TEARENV_TOKEN)")
	flags.StringVar(&options.privateKeyPath, "private-key", "", "override the saved SSH private key")
	flags.StringVar(&options.knownHostsPath, "known-hosts", "", "override the saved known_hosts file")
	flags.BoolVar(&options.insecure, "insecure-skip-host-key-check", false, "disable host identity verification (development only)")
	return command
}
