# tearenv

`tearenv` gives developers secure localhost access to private TCP services. The `tearenvd` gateway authorizes service names, hides private targets, and can scale Kubernetes workloads up on connection and down after inactivity.

```text
local app -> localhost -> tearenv -> SSH -> tearenvd -> private service
```

The repository builds two programs:

- `tearenv` logs in, lists granted services, and opens local ports.
- `tearenvd` runs the gateway, creates invites, and grants services.

## Build the binaries

```sh
make build
```

This creates `bin/tearenv` and `bin/tearenvd`.

## Set up the gateway

Create an identity and grant a service:

```sh
INVITE=$(./bin/tearenvd invite --identity alice)

./bin/tearenvd service grant \
  --identity alice \
  --name postgres \
  --target postgres.internal:5432
```

Start the gateway:

```sh
./bin/tearenvd serve --listen :2222
```

## Connect as a developer

Verify the gateway's SSH host key, then redeem the one-time invite:

```sh
tearenv login \
  --identity alice \
  --server gateway.example.com:2222 \
  --invite "$INVITE"
```

List and connect the granted services:

```sh
tearenv services
tearenv connect
```

Applications can now use the displayed localhost ports.

Token invites are the default authentication method. Kubernetes deployments can instead keep a generated private key on the developer machine and register only its public key in a mounted Secret. See [choose an authentication method](docs/authentication.md).

## Read the documentation

Start with the [documentation index](docs/README.md), or go directly to the [local walkthrough](docs/getting-started.md), [developer guide](docs/developer-guide.md), [operator guide](docs/operator-guide.md), or [troubleshooting guide](docs/troubleshooting.md).

## Run the checks

```sh
make check
```

Run the opt-in Kubernetes end-to-end test with Docker, Kind, and kubectl installed:

```sh
make e2e-kind
```
