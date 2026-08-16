package main

import (
	"time"

	"github.com/spf13/cobra"
)

const (
	defaultListenAddress         = ":2222"
	defaultAPIAddress            = "127.0.0.1:8080"
	defaultMetricsAddress        = ":9090"
	defaultHostKeyPath           = ".data/ssh_host_ed25519_key"
	defaultUsersPath             = ".data/users.json"
	defaultRegistrationsPath     = ".data/registrations"
	defaultRegistrationNamespace = "default"
	defaultBlueprintName         = "developer-environment"
)

type grantOptions struct {
	usersPath         string
	identity          string
	name              string
	target            string
	localPort         uint
	workloadKind      string
	workloadNamespace string
	workloadName      string
	replicas          int
	readyTimeout      time.Duration
	idleTimeout       time.Duration
}

type blueprintInitOptions struct {
	name string
}

type serveOptions struct {
	listenAddress         string
	apiAddress            string
	metricsAddress        string
	hostKeyPath           string
	usersPath             string
	registrationsPath     string
	registrationNamespace string
	registrationTokenPath string
	blueprintPath         string
	scalerName            string
	kubernetes            bool
}

func run(arguments []string) error {
	command := newRootCommand()
	command.SetArgs(arguments)
	return command.Execute()
}

func newRootCommand() *cobra.Command {
	command := &cobra.Command{
		Use:           "tearenvd",
		Short:         "Run and administer the tearenv service gateway",
		SilenceErrors: true,
		SilenceUsage:  true,
	}
	command.AddCommand(
		newServeCommand(),
		newServiceCommand(),
		newBlueprintCommand(),
	)
	return command
}

func newBlueprintCommand() *cobra.Command {
	command := &cobra.Command{
		Use:   "blueprint",
		Short: "Manage team environment blueprints",
	}
	command.AddCommand(newBlueprintInitCommand())
	return command
}

func newBlueprintInitCommand() *cobra.Command {
	options := blueprintInitOptions{name: defaultBlueprintName}
	command := &cobra.Command{
		Use:   "init",
		Short: "Write a starter environment blueprint",
		Args:  cobra.NoArgs,
		RunE: func(command *cobra.Command, _ []string) error {
			return initializeBlueprint(options, command.OutOrStdout())
		},
	}
	command.Flags().StringVar(&options.name, "name", options.name, "blueprint metadata name")
	return command
}

func newServiceCommand() *cobra.Command {
	command := &cobra.Command{
		Use:   "service",
		Short: "Manage identity-bound services",
	}
	command.AddCommand(newGrantCommand())
	return command
}

func newGrantCommand() *cobra.Command {
	options := grantOptions{
		usersPath:    defaultUsersPath,
		replicas:     1,
		readyTimeout: 2 * time.Minute,
		idleTimeout:  10 * time.Minute,
	}
	command := &cobra.Command{
		Use:   "grant",
		Short: "Grant an identity access to a named service",
		Args:  cobra.NoArgs,
		RunE: func(_ *cobra.Command, _ []string) error {
			return grantService(options)
		},
	}
	flags := command.Flags()
	flags.StringVar(&options.usersPath, "users", options.usersPath, "authentication credentials and access policy JSON file")
	flags.StringVar(&options.identity, "identity", "", "developer identity receiving access")
	flags.StringVar(&options.name, "name", "", "client-visible service name")
	flags.StringVar(&options.target, "target", "", "server-side service DNS name and port")
	flags.UintVar(&options.localPort, "local-port", 0, "suggested client-side port; defaults to target port")
	flags.StringVar(&options.workloadKind, "workload-kind", "", "scaler-specific workload kind (Kubernetes: deployment or statefulset)")
	flags.StringVar(&options.workloadNamespace, "workload-namespace", "", "scaler-specific workload namespace, if applicable")
	flags.StringVar(&options.workloadName, "workload-name", "", "scaler-specific workload name")
	flags.IntVar(&options.replicas, "replicas", options.replicas, "replicas to start on first connection")
	flags.DurationVar(&options.readyTimeout, "ready-timeout", options.readyTimeout, "maximum service startup wait")
	flags.DurationVar(&options.idleTimeout, "idle-timeout", options.idleTimeout, "idle period before scaling to zero")
	mustMarkRequired(command, "identity", "name", "target")
	return command
}

func newServeCommand() *cobra.Command {
	options := serveOptions{
		listenAddress:         defaultListenAddress,
		apiAddress:            defaultAPIAddress,
		metricsAddress:        defaultMetricsAddress,
		hostKeyPath:           defaultHostKeyPath,
		usersPath:             defaultUsersPath,
		registrationsPath:     defaultRegistrationsPath,
		registrationNamespace: defaultRegistrationNamespace,
	}
	command := &cobra.Command{
		Use:   "serve",
		Short: "Run the SSH service gateway",
		Args:  cobra.NoArgs,
		RunE: func(command *cobra.Command, _ []string) error {
			return serve(command.Context(), options)
		},
	}
	flags := command.Flags()
	flags.StringVar(&options.listenAddress, "listen", options.listenAddress, "SSH listen address")
	flags.StringVar(&options.apiAddress, "api-listen", options.apiAddress, "registration API listen address; empty disables HTTP serving")
	flags.StringVar(&options.metricsAddress, "metrics-listen", options.metricsAddress, "Prometheus metrics listen address; empty disables metrics")
	flags.StringVar(&options.hostKeyPath, "host-key", options.hostKeyPath, "persistent SSH host private key")
	flags.StringVar(&options.usersPath, "users", options.usersPath, "identity-bound service policy JSON file")
	flags.StringVar(&options.registrationsPath, "registrations", options.registrationsPath, "durable UserRegistration store directory")
	flags.StringVar(&options.registrationNamespace, "registration-namespace", options.registrationNamespace, "namespace used for SSH authentication")
	flags.StringVar(&options.registrationTokenPath, "registration-token-file", "", "file containing the registration API bearer token; empty disables API authentication")
	flags.StringVar(&options.blueprintPath, "blueprint", "", "team environment blueprint provisioned for every authenticated identity")
	flags.StringVar(&options.scalerName, "scaler", "", "workload scaler backend (supported: kubernetes)")
	flags.BoolVar(&options.kubernetes, "kubernetes", false, "deprecated alias for --scaler kubernetes")
	if err := flags.MarkDeprecated("kubernetes", "use --scaler kubernetes instead"); err != nil {
		panic(err)
	}
	return command
}

func mustMarkRequired(command *cobra.Command, names ...string) {
	for _, name := range names {
		if err := command.MarkFlagRequired(name); err != nil {
			panic(err)
		}
	}
}
