# Look up commands and file formats

Both programs use Cobra for commands, flags, help, suggestions, and shell completion. Run any command with `--help` to see its current usage. Flags use `--name value` syntax and can appear before or after positional service names.

Long flags require two dashes. If you're upgrading a script written for the original flag parser, change forms such as `-config` and `-users` to `--config` and `--users`. The `-h` help shorthand remains available.

Generate a completion script for Bash, Zsh, Fish, or PowerShell with either binary:

```sh
tearenv completion --help
tearenvd completion --help
```

## Use the tearenv commands

### `tearenv login`

Creates or reuses an Ed25519 key, submits a `UserRegistration` to the resource API, and writes a local profile after the API returns `Accepted=True`.

| Option                           | Default                                                     | Meaning                                                   |
| -------------------------------- | ----------------------------------------------------------- | --------------------------------------------------------- |
| `--api-url`                      | `http://127.0.0.1:8080`                                     | Base URL of the registration resource API.                |
| `--namespace`                    | `default`                                                   | Namespace containing the registration resource.           |
| `--server`                       | `127.0.0.1:2222`                                            | SSH gateway address saved in the profile.                  |
| `--identity`                     | Prompt with the local hostname                              | tearenv identity; supplying it skips the prompt.           |
| `--private-key`                  | OS config directory plus `tearenv/id_ed25519`               | Ed25519 private key created or reused locally.             |
| `--registration`                 | OS config directory plus `tearenv/user-registration.yaml`   | Local copy of the submitted and observed resource.         |
| `--config`                       | OS config directory plus `tearenv/config.json`              | Profile written after API acceptance.                      |
| `--known-hosts`                  | `~/.ssh/known_hosts` when a home directory is available     | OpenSSH known-hosts file saved in the profile.             |
| `--insecure-skip-host-key-check` | `false`                                                     | Disables gateway identity verification for development.    |

### `tearenv services`

Authenticates with the saved profile and prints `name<TAB>127.0.0.1:port` for each current grant.

| Option     | Default                                        | Meaning                     |
| ---------- | ---------------------------------------------- | --------------------------- |
| `--config` | OS config directory plus `tearenv/config.json` | Profile created by `login`. |

### `tearenv connect [name[=host:port] ...]`

Opens local TCP listeners for all granted services, or for the listed aliases. An explicit `host:port` overrides the grant's suggested local port.

| Option                           | Default                                        | Meaning                                                                                  |
| -------------------------------- | ---------------------------------------------- | ---------------------------------------------------------------------------------------- |
| `--config`                       | OS config directory plus `tearenv/config.json` | Profile created by `login`.                                                              |
| `--listen-host`                  | `127.0.0.1`                                    | Host used with suggested ports.                                                          |
| `--server`                       | Saved value                                    | Temporary gateway override.                                                              |
| `--identity`                     | Saved value                                    | Temporary identity override.                                                             |
| `--private-key`                  | Saved value                                    | Temporary SSH private-key override.                                                      |
| `--known-hosts`                  | Saved value                                    | Temporary known-hosts override.                                                          |
| `--insecure-skip-host-key-check` | Saved value                                    | Enables insecure verification for this run. It can't turn off an insecure saved profile. |

Requesting the same alias more than once is rejected. With no positional aliases, the client selects every grant in sorted order. With no grants, it exits instead of opening an empty tunnel.

## Use the tearenvd commands

Running `tearenvd` without a subcommand displays help. Use `tearenvd serve` to start the gateway.

### `tearenvd blueprint init`

Writes a team-owned `tearenv.io/v1alpha1` `EnvironmentBlueprint` to standard output. Redirect it to a file, then edit the reusable Kubernetes resources and service policies that the gateway will provision for authenticated identities.

The generated file is tearenv configuration, not a Kubernetes CRD. The command doesn't contact Kubernetes. Pass the reviewed file to `tearenvd serve --blueprint` to enable provisioning.

| Option   | Default                 | Meaning                  |
| -------- | ----------------------- | ------------------------ |
| `--name` | `developer-environment` | Blueprint metadata name. |

### `tearenvd service grant`

| Option                 | Default                       | Meaning                                                                         |
| ---------------------- | ----------------------------- | ------------------------------------------------------------------------------- |
| `--users`              | `.data/users.json`            | Credential and policy file.                                                     |
| `--identity`           | Required                      | Identity receiving the grant. It may use an external authentication provider.   |
| `--name`               | Required                      | Client-visible lowercase alias.                                                 |
| `--target`             | Required                      | Gateway-reachable `host:port`. Use `[IPv6]:port` for IPv6 literals.             |
| `--local-port`         | Target port                   | Suggested client-side port, from 1 through 65535.                               |
| `--workload-kind`      | None                          | Enables lifecycle management. Kubernetes accepts `deployment` or `statefulset`. |
| `--workload-namespace` | None                          | Backend-specific namespace; required by the Kubernetes scaler.                  |
| `--workload-name`      | Required with a workload kind | Backend-specific workload name.                                                 |
| `--replicas`           | `1`                           | Desired replicas on the first connection.                                       |
| `--ready-timeout`      | `2m`                          | Maximum wait for target TCP readiness.                                          |
| `--idle-timeout`       | `10m`                         | Delay after the final connection closes before scale-down.                      |

Durations use Go syntax such as `500ms`, `30s`, `2m`, or `1h30m`. Negative timeouts are rejected. An idle timeout of `0` requests immediate scale-down after the final connection closes.

### `tearenvd serve`

| Option                     | Default                      | Meaning                                                                  |
| -------------------------- | ---------------------------- | ------------------------------------------------------------------------ |
| `--listen`                 | `:2222`                      | SSH listen address.                                                      |
| `--api-listen`             | `127.0.0.1:8080`             | Registration HTTP address; an empty value disables HTTP serving.         |
| `--metrics-listen`         | `:9090`                      | Prometheus HTTP address; an empty value disables metrics.                |
| `--host-key`               | `.data/ssh_host_ed25519_key` | Persistent SSH private host key.                                         |
| `--users`                  | `.data/users.json`           | Identity-bound service policy file.                                      |
| `--registrations`          | `.data/registrations`        | Durable `UserRegistration` directory.                                    |
| `--registration-namespace` | `default`                    | Registration namespace used for SSH authentication.                      |
| `--blueprint`              | None                         | Team blueprint reconciled for every successfully authenticated identity. |
| `--scaler`                 | None                         | Scaler backend. The included value is `kubernetes`.                      |
| `--kubernetes`             | `false`                      | Deprecated alias for `--scaler kubernetes`.                              |

`--blueprint` requires `--scaler kubernetes` and in-cluster Kubernetes credentials. The daemon loads and validates the file at startup. Accepted public-key authentication applies the identity namespace and resources before accepting the SSH login.

## Understand the client profile

The client profile is JSON and must have mode `0600`; any group or world permission causes `tearenv` to reject it. The current login flow writes a private-key profile:

```json
{
  "server": "gateway.example.com:2222",
  "identity": "alice",
  "private_key": "/home/alice/.config/tearenv/id_ed25519",
  "known_hosts": "/home/alice/.ssh/known_hosts"
}
```

An insecure development profile additionally contains:

```json
{
  "server": "127.0.0.1:2222",
  "identity": "alice",
  "private_key": "/home/alice/.config/tearenv/id_ed25519",
  "insecure_skip_host_key_check": true
}
```

Don't commit, distribute, or log the profile or private key. The private key file and profile must both have mode `0600`.

## Understand the service policy file

The server file is JSON and must have mode `0600`. Administrative commands create its parent directory with mode `0700` and replace the file atomically.

```json
{
  "access": {
    "alice": {
      "services": {
        "postgres": {
          "target": "postgres.dev-alice.svc.cluster.local:5432",
          "local_port": 5432,
          "workload": {
            "kind": "statefulset",
            "namespace": "dev-alice",
            "name": "postgres",
            "replicas": 1,
            "ready_timeout": "2m0s",
            "idle_timeout": "10m0s"
          }
        }
      }
    }
  }
}
```

Don't expose this file to clients. It contains private targets, identity policy, and workload metadata. `tearenvd service grant` creates it when the configured path doesn't exist.

Registration resources are stored separately under the `--registrations` directory. Each namespaced YAML file contains public keys and the server-owned accepted status.

## Apply naming and value rules

| Value                | Rule                                                 |
| -------------------- | ---------------------------------------------------- |
| Identity             | `^[A-Za-z0-9][A-Za-z0-9._@-]{0,63}$`                 |
| Service alias        | `^[a-z][a-z0-9-]{0,31}$`                             |
| Target               | Nonempty host and numeric port from 1 through 65535. |
| Suggested local port | 1 through 65535; omitted means the target port.      |
| Workload replicas    | 1 through 2,147,483,647 from the CLI.                |
| Kubernetes kind      | Exactly `deployment` or `statefulset`.               |

The catalog returned to clients contains only `name` and `local_port`.

## Use the supported environment variables

| Variable                    | Used by    | Purpose                                              |
| --------------------------- | ---------- | ---------------------------------------------------- |
| `TEARENV_KIND_E2E`          | Test suite | Set to `1` to opt into the Kind test.                |
| `TEARENV_KEEP_KIND_CLUSTER` | Kind test  | Set to `1` to retain the test cluster for debugging. |
