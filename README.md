# tearenv

`tearenv` gives developers secure localhost access to private TCP services. The `tearenvd` gateway authorizes service names, hides private targets, and can scale Kubernetes workloads up on connection and down after inactivity. A team blueprint can also create an isolated Kubernetes namespace and service environment for each authenticated developer.

```text
local app -> localhost -> tearenv -> SSH -> tearenvd -> private service
```

The repository builds two programs:

- `tearenv` registers a local SSH key, lists granted services, and opens local ports.
- `tearenvd` serves registrations, runs the gateway, grants services, and provisions team blueprints.

## Build the binaries

```sh
make build
```

This creates `bin/tearenv` and `bin/tearenvd`.

## Publish a release image

Push a semantic version tag to publish the multi-architecture image to GitHub Container Registry:

```sh
git tag v0.1.0
git push origin v0.1.0
```

Only pushed tags matching `v<major>.<minor>.<patch>` start the release workflow. A stable tag such as `v1.2.3` publishes `v1.2.3`, `1.2.3`, `1.2`, `1`, and `latest` image tags under `ghcr.io/<owner>/<repository>`. Branch pushes and pull requests build the image in CI but never push it.

## Deploy on Kubernetes

The repository includes a production Dockerfile, Helm chart, and Kustomize overlays:

```sh
docker build -t registry.example.com/tearenv:0.1.0 .
helm lint deploy/helm/tearenv
kubectl kustomize deploy/kustomize/overlays/default
```

Follow [the Kubernetes deployment guide](docs/kubernetes-deployment.md) to prepare policy state, persistent storage, scaler permissions, public-key authentication, and an external SSH Service.

## Set up the gateway

Grant an identity a service:

```sh
./bin/tearenvd service grant \
  --identity alice \
  --name postgres \
  --target postgres.internal:5432
```

Start the gateway:

```sh
./bin/tearenvd serve --listen :2222 --api-listen :8080
```

Initialize a team-owned environment blueprint:

```sh
./bin/tearenvd blueprint init \
  --name web-development \
  > web-development.yaml
```

Mount the reviewed file into the gateway pod, then start `tearenvd` with Kubernetes scaling and blueprint provisioning:

```sh
./bin/tearenvd serve \
  --scaler kubernetes \
  --blueprint /etc/tearenv/web-development.yaml
```

Each successful SSH login reconciles a separate namespace for that authenticated identity. The current daemon configures one blueprint; choosing among multiple team templates from the developer client is still planned.

## Connect as a developer

Verify the gateway's SSH host key, then submit a public-key registration:

```sh
tearenv login \
  --api-url https://tearenv-api.example.com \
  --identity alice \
  --server gateway.example.com:2222
```

List and connect the granted services:

```sh
tearenv services
tearenv connect
```

Applications can now use the displayed localhost ports.

The private key remains under the local tearenv configuration directory. `tearenvd` persists the accepted resource under `.data/registrations` by default and uses it for SSH public-key authentication. See [register with one SSH key](docs/authentication.md).

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
