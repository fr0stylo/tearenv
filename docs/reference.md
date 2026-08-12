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

Creates a token or Kubernetes public-key login and atomically writes a local profile.

| Option                           | Default                                                 | Meaning                                                 |
| -------------------------------- | ------------------------------------------------------- | ------------------------------------------------------- |
| `--method`                       | `token`                                                 | Login method: `token` or `kubernetes`.                  |
| `--server`                       | `127.0.0.1:2222`                                        | SSH gateway address.                                    |
| `--identity`                     | Current OS account name, or `tunnel` if unavailable     | Gateway identity.                                       |
| `--invite`                       | `TEARENV_INVITE`                                        | One-time invite for token login.                        |
| `--private-key`                  | OS config directory plus `tearenv/id_ed25519`           | Private key created or reused for Kubernetes login.     |
| `--config`                       | OS config directory plus `tearenv/config.json`          | Profile destination.                                    |
| `--known-hosts`                  | `~/.ssh/known_hosts` when a home directory is available | OpenSSH known-hosts file.                               |
| `--kubeconfig`                   | Standard kubeconfig loading                             | Explicit kubeconfig for Kubernetes login.               |
| `--kubernetes-context`           | Current kubeconfig context                              | Context used to update the authorized-keys Secret.      |
| `--kubernetes-namespace`         | `tearenv-system`                                        | Namespace containing the authorized-keys Secret.        |
| `--kubernetes-secret`            | `tearenv-authorized-keys`                               | Authorized-keys Secret name.                            |
| `--insecure-skip-host-key-check` | `false`                                                 | Disables gateway identity verification for development. |

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
| `--token`                        | `TEARENV_TOKEN`, then saved value              | Temporary personal-token override.                                                       |
| `--private-key`                  | Saved value                                    | Temporary SSH private-key override.                                                      |
| `--known-hosts`                  | Saved value                                    | Temporary known-hosts override.                                                          |
| `--insecure-skip-host-key-check` | Saved value                                    | Enables insecure verification for this run. It can't turn off an insecure saved profile. |

Requesting the same alias more than once is rejected. With no positional aliases, the client selects every grant in sorted order. With no grants, it exits instead of opening an empty tunnel.

## Use the tearenvd commands

Running `tearenvd` without a subcommand displays help. Use `tearenvd serve` to start the gateway.

### `tearenvd invite`

| Option       | Default            | Meaning                     |
| ------------ | ------------------ | --------------------------- |
| `--users`    | `.data/users.json` | Credential and policy file. |
| `--identity` | Required           | Identity to invite.         |

The command prints the plaintext invite to standard output. Issuing a new invite removes any older pending invite for that identity.

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

| Option              | Default                      | Meaning                                                        |
| ------------------- | ---------------------------- | -------------------------------------------------------------- |
| `--listen`          | `:2222`                      | SSH listen address.                                            |
| `--metrics-listen`  | `:9090`                      | Prometheus HTTP listen address; an empty value disables it.    |
| `--host-key`        | `.data/ssh_host_ed25519_key` | Persistent SSH private host key.                               |
| `--users`           | `.data/users.json`           | Credential and policy file.                                    |
| `--authorized-keys` | None                         | Identity-bound public-key JSON, usually from a mounted Secret. |
| `--scaler`          | None                         | Scaler backend. The included value is `kubernetes`.            |
| `--kubernetes`      | `false`                      | Deprecated alias for `--scaler kubernetes`.                    |

## Understand the client profile

The client profile is JSON and must have mode `0600`; any group or world permission causes `tearenv` to reject it.

```json
{
  "server": "gateway.example.com:2222",
  "identity": "alice",
  "token": "tu_redacted",
  "known_hosts": "/home/alice/.ssh/known_hosts"
}
```

An insecure development profile instead contains:

```json
{
  "server": "127.0.0.1:2222",
  "identity": "alice",
  "token": "tu_redacted",
  "insecure_skip_host_key_check": true
}
```

Don't commit, distribute, or log this file. The token authenticates as its identity.

A Kubernetes public-key profile stores a private-key path instead of a token:

```json
{
  "server": "gateway.example.com:2222",
  "identity": "alice",
  "private_key": "/home/alice/.config/tearenv/id_ed25519",
  "known_hosts": "/home/alice/.ssh/known_hosts"
}
```

The private key file and profile must both have mode `0600`.

## Understand the credential and policy file

The server file is JSON and must have mode `0600`. Administrative commands create its parent directory with mode `0700` and replace the file atomically.

```json
{
  "users": {
    "alice": {
      "token_hash": "sha256-hex-redacted"
    }
  },
  "invites": {
    "sha256-hex-redacted": {
      "identity": "bob"
    }
  },
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

The file stores SHA256 hashes rather than plaintext personal tokens and invites. Older files with a plaintext `token` field of at least 16 characters are accepted for migration and rewritten as hashes on the next administrative mutation.

Don't expose this file to clients. Even without plaintext secrets, it contains private targets, identity policy, and workload metadata.

The store may contain only `access` entries when every identity uses an external authentication provider. `tearenvd service grant` creates a new policy file when the configured path doesn't exist.

## Understand the authorized-keys document

`--authorized-keys` reads a JSON object from identity to one or more OpenSSH public keys:

```json
{
  "alice": ["ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAA..."],
  "bob": ["ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAA..."]
}
```

The file may be read-only or world-readable because it contains public material, but it must not be group- or world-writable. The gateway validates the full document at startup and reloads it for every public-key authentication attempt.

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

| Variable                    | Used by           | Purpose                                              |
| --------------------------- | ----------------- | ---------------------------------------------------- |
| `TEARENV_INVITE`            | `tearenv login`   | Supplies the invite when `--invite` isn't set.       |
| `TEARENV_TOKEN`             | `tearenv connect` | Overrides the saved token when `--token` isn't set.  |
| `TEARENV_KIND_E2E`          | Test suite        | Set to `1` to opt into the Kind test.                |
| `TEARENV_KEEP_KIND_CLUSTER` | Kind test         | Set to `1` to retain the test cluster for debugging. |
