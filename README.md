# tearenv

`tearenv` gives developers secure localhost access to private TCP services. The `tearenvd` gateway authorizes service names, hides private targets, and can scale Kubernetes workloads up on connection and down after inactivity. Teams can also initialize reusable Kubernetes environment blueprints that will become the catalog developers select from.

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

## Deploy on Kubernetes

The repository includes a production Dockerfile, Helm chart, and Kustomize overlays:

```sh
docker build -t registry.example.com/tearenv:0.1.0 .
helm lint deploy/helm/tearenv
kubectl kustomize deploy/kustomize/overlays/default
```

Follow [the Kubernetes deployment guide](docs/kubernetes-deployment.md) to prepare policy state, persistent storage, scaler permissions, public-key authentication, and an external SSH Service.

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

Initialize a team-owned environment blueprint:

```sh
./bin/tearenvd blueprint init \
  --name web-development \
  > web-development.yaml
```

Blueprint initialization is available now. Applying a blueprint and requesting one from the developer client are the next implementation slice.

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

Start with the [documentation index](docs/README.md), or go directly to the [local walkthrough](docs/getting-started.md), [developer guide](docs/developer-guide.md), [operator guide](docs/operator-guide.md), [environment blueprint guide](docs/environment-blueprints.md), or [troubleshooting guide](docs/troubleshooting.md).

## Run the checks

```sh
make check
```

Run the opt-in Kubernetes end-to-end test with Docker, Kind, and kubectl installed:

```sh
make e2e-kind
```
