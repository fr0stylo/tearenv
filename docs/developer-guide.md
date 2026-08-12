# Use tearenv as a developer

Your operator should give you three things: an identity, a one-time invite, and a gateway address. Verify the gateway host key through a trusted channel before redeeming the invite.

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

## Redeem an invite once

Run:

```sh
tearenv login \
  --identity alice \
  --server gateway.example.com:2222 \
  --invite "$INVITE"
```

You can avoid putting the invite in shell history by using the environment:

```sh
TEARENV_INVITE="$INVITE" tearenv login \
  --identity alice \
  --server gateway.example.com:2222
```

On Linux, the default profile is typically `~/.config/tearenv/config.json`. The exact location follows the operating system's user configuration directory. `tearenv` creates the directory with mode `0700` and the profile with mode `0600`.

An invite can be redeemed only once. A successful login exchanges it for a personal token; it doesn't save the invite. Ask the operator for a new invite if login was completed on the wrong machine or the profile was lost.

If the gateway uses Kubernetes-managed SSH keys, run `tearenv login --method kubernetes` instead. The command keeps the private key on your machine and registers only its public key. Follow [the authentication guide](authentication.md) for the required flags and Kubernetes permissions.

## See only your granted services

Run:

```sh
tearenv services
```

The catalog contains service aliases and suggested local ports, sorted by alias. It doesn't include private targets, workload names, or scaling configuration.

An operator can add or replace grants while the gateway is running. Run `tearenv services` again to fetch current policy.

## Expect team blueprint selection in a later release

Teams can initialize reusable environment blueprints, but the current developer client can't list or request them yet. Operators still create identity-bound grants before services appear in `tearenv services`.

The planned flow will let you select a team-approved blueprint by name. The gateway will use your authenticated identity to create a separate namespace for that selection. You won't send an identity in the environment request, and you won't upload or modify the team's blueprint.

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
  --config ~/.config/tearenv/staging.json \
  --identity alice \
  --server staging-gateway.example.com:2222 \
  --invite "$STAGING_INVITE"

tearenv services --config ~/.config/tearenv/staging.json
tearenv connect --config ~/.config/tearenv/staging.json postgres
```

`connect` can temporarily override the saved server, identity, token, private key, or `known_hosts` path. `TEARENV_TOKEN` is used only when `--token` isn't set, and either value overrides the saved token. These overrides are useful for automation, but keeping tokens in environment variables can expose them to local process inspection or debug output.

## Expect cold starts and idle behavior

The first connection to a sleeping service remains pending while the gateway starts the workload and waits for TCP readiness. It fails when the grant's `ready-timeout` expires.

Scale-down begins only after every proxied TCP connection closes. Database and gRPC pools often keep connections open even when no query is running. Configure pool lifetimes or close the application when you want the workload to reach zero.

Check [the troubleshooting guide](troubleshooting.md) when login, authentication, binding, or target startup fails.
