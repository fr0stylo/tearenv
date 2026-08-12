# Use tearenv as a developer

Your operator should give you the registration API URL, its namespace, the gateway address, and the gateway host-key fingerprint. The login prompt uses your local hostname as the default identity; use the operator-assigned identity when one was provided.

## Install the client

This repository currently distributes the client as source. Build it with the Go version from `go.mod`:

```sh
go build -o bin/tearenv ./cmd/tearenv
```

Put `bin/tearenv` on your `PATH` if you want to run it without the path prefix.

## Verify the gateway before login

Fetch the public host key, then compare its fingerprint with the value supplied by the operator:

```sh
mkdir -p ~/.ssh
ssh-keyscan -p 2222 gateway.example.com >> ~/.ssh/known_hosts
ssh-keygen -lf ~/.ssh/known_hosts
```

`ssh-keyscan` doesn't authenticate the key by itself. The independent fingerprint comparison is the step that establishes trust.

If the gateway address contains a nonstandard port, keep that exact host and port in the profile. OpenSSH writes a bracketed entry such as `[gateway.example.com]:2222` to `known_hosts`.

## Register one local SSH key

Run:

```sh
tearenv login \
  --api-url https://tearenv-api.example.com \
  --namespace default \
  --identity alice \
  --server gateway.example.com:2222
```

Omit `--identity` to use the interactive prompt:

```sh
tearenv login \
  --api-url https://tearenv-api.example.com \
  --server gateway.example.com:2222
```

The command creates or reuses `id_ed25519`, writes `user-registration.yaml`, and submits the public resource to the API. On Linux these files and `config.json` are typically under `~/.config/tearenv`. The exact location follows the operating system's user configuration directory.

The API is authoritative for registration state. The current tearenvd API accepts a valid first registration immediately and `config.json` is written after its `Accepted=True` response. Repeating login with the same key is idempotent.

Keep `id_ed25519` private. The registration document contains only its public key. Follow [the authentication guide](authentication.md) for the HTTP path and trust boundary.

## See only your granted services

Run:

```sh
tearenv services
```

The catalog contains service aliases and suggested local ports, sorted by alias. It doesn't include private targets, workload names, or scaling configuration.

An operator can add or replace grants while the gateway is running. Run `tearenv services` again to fetch current policy.

## Receive your team environment at login

When the operator enables a team blueprint, every accepted SSH login reconciles a namespace for your authenticated identity. For example, identity `alice` and blueprint `web-development` use `tearenv-alice-web-development`. Another identity receives another namespace.

Blueprint services appear in `tearenv services` after reconciliation succeeds. You don't supply a namespace or upload Kubernetes resources. A provisioning error rejects the login instead of returning a partially available catalog.

The current gateway applies one operator-configured blueprint automatically. Selecting one of several team templates from the developer client isn't implemented yet.

## Connect all services on suggested ports

Run:

```sh
tearenv connect
```

With no service arguments, `tearenv` binds every granted alias to `127.0.0.1` and its suggested port. A `service ready` log means the local listener is open; it doesn't mean a scale-to-zero target is already running. The first application connection triggers startup.

Keep `tearenv connect` running while your local applications need the services. `Ctrl+C` or `SIGTERM` closes its SSH connection and all local listeners cleanly.

## Select services and override local addresses

Pass an alias to connect only that service:

```sh
tearenv connect postgres redis
```

Use `name=host:port` to override one local listener:

```sh
tearenv connect \
  postgres=127.0.0.1:15432 \
  redis=127.0.0.1:16379 \
  grpc
```

Use `--listen-host` to change the host for aliases that don't have explicit overrides:

```sh
tearenv connect --listen-host 127.0.0.2 postgres redis
```

Cobra accepts flags before or after service arguments. The examples put flags first for consistency.

Don't bind to `0.0.0.0` or another non-loopback address unless you intend to expose the local listener to other machines and have secured the host firewall. tearenv authenticates the SSH connection, but it doesn't authenticate applications connecting to the local listener.

## Use more than one profile

Use `--config` when you need separate gateways or identities:

```sh
tearenv login \
  --api-url https://staging-tearenv-api.example.com \
  --config ~/.config/tearenv/staging.json \
  --registration ~/.config/tearenv/staging-registration.yaml \
  --private-key ~/.config/tearenv/staging-id_ed25519 \
  --identity alice \
  --server staging-gateway.example.com:2222

tearenv services --config ~/.config/tearenv/staging.json
tearenv connect --config ~/.config/tearenv/staging.json postgres
```

`connect` can temporarily override the saved server, identity, private key, or `known_hosts` path. Keep separate private-key and registration paths when profiles represent different identities.

## Expect cold starts and idle behavior

The first connection to a sleeping service remains pending while the gateway starts the workload and waits for TCP readiness. It fails when the grant's `ready-timeout` expires.

Scale-down begins only after every proxied TCP connection closes. Database and gRPC pools often keep connections open even when no query is running. Configure pool lifetimes or close the application when you want the workload to reach zero.

Check [the troubleshooting guide](troubleshooting.md) when login, authentication, binding, or target startup fails.
