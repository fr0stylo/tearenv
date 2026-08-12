# Run tearenvd

`tearenvd` combines an SSH gateway with administrative commands for invites and service grants. Use one protected policy file and one stable SSH host key for a gateway deployment. Authentication can use invite-issued tokens, Kubernetes-managed public keys, or both.

## Build and prepare storage

Build both binaries:

```sh
make build
```

The default state paths are relative to the process working directory:

```text
.data/users.json
.data/ssh_host_ed25519_key
```

Use explicit absolute paths in a service manager or container. The credential file must be writable during invite redemption because enrollment consumes an invite and persists a new token hash. Both files contain security-sensitive state and should be readable only by the gateway account.

## Create authentication or policy before starting a new gateway

A new credential file is created by the first invite:

```sh
INVITE=$(./bin/tearenvd invite \
  --users /var/lib/tearenv/users.json \
  --identity alice)
```

`tearenvd serve` won't start with a missing or empty policy file. Token deployments can create the file with an invite. External-authentication deployments can create it with the first `service grant`. The identity must match `^[A-Za-z0-9][A-Za-z0-9._@-]{0,63}$`.

Only a hash of the invite is stored. Creating another invite for the same identity invalidates its previous pending invite. If the identity is already registered, its current personal token keeps working until the new invite is redeemed; successful redemption rotates the personal token.

Send the plaintext invite through a protected channel. It is a bearer secret until redeemed. Skip the invite when the identity will use an external authentication provider such as the Kubernetes public-key flow.

## Grant a static service

Grant an identity before or after it redeems the invite:

```sh
./bin/tearenvd service grant \
  --users /var/lib/tearenv/users.json \
  --identity alice \
  --name grpc \
  --target api-alice.dev-alice.svc.cluster.local:50051 \
  --local-port 50051
```

A static service is expected to be running. `tearenvd` dials it once for each developer connection and returns a generic unavailable error if that dial fails.

The alias must match `^[a-z][a-z0-9-]{0,31}$`. The target must be a reachable `host:port` from the gateway. When `--local-port` is omitted, it defaults to the target port.

Grants are identity-bound and keyed by alias. Running the command again with the same identity and alias replaces that grant. The running gateway reloads grants when a client requests the catalog or opens a service, so a restart isn't required.

## Start the gateway

Run:

```sh
./bin/tearenvd serve \
  --listen :2222 \
  --metrics-listen :9090 \
  --users /var/lib/tearenv/users.json \
  --host-key /var/lib/tearenv/ssh_host_ed25519_key
```

If the host key doesn't exist, `tearenvd` creates a persistent Ed25519 private key with mode `0600`. Back it up and mount it persistently. Replacing the key triggers host-key warnings for every existing client and must be communicated as a deliberate rotation.

The startup log reports the bound address, credential path, scaler name, and public-key fingerprint. Publish the fingerprint through a trusted channel so developers can verify it before login.

To enable SSH public keys from a mounted Kubernetes Secret, pass `--authorized-keys` with the mounted `authorized_keys.json` path. Follow [the authentication guide](authentication.md) for the Secret format, client registration command, and required security controls.

The process handles `SIGINT` and `SIGTERM`. On shutdown it closes the SSH listener and attempts to scale down workloads started by that process, allowing up to 30 seconds for each scaler call.

## Scrape Prometheus metrics

The daemon serves Prometheus metrics at `/metrics` on port `9090` by default. Set `--metrics-listen` to another address, or set it to an empty string to disable the HTTP listener:

```sh
tearenvd serve --metrics-listen 127.0.0.1:9090
curl http://127.0.0.1:9090/metrics
```

The endpoint automatically includes Go runtime, process, and scrape-handler metrics. The tearenv collectors use bounded labels: they distinguish results, authentication methods, scale directions, and static versus managed services, but don't put identities, service aliases, targets, or workload names in labels.

The metrics endpoint doesn't require authentication. Keep it on a private interface or cluster-internal Service, and restrict access with a firewall or NetworkPolicy when other workloads shouldn't see process telemetry.

| Metric | What it shows |
| --- | --- |
| `tearenv_daemon_ready` | `1` after both configured listeners are bound; `0` during startup and shutdown. |
| `tearenv_ssh_authentication_attempts_total` | Authentication attempts by `method` and `result`. |
| `tearenv_ssh_handshake_failures_total` | Connections that failed before completing the SSH handshake. |
| `tearenv_engine_service_catalog_requests_total` | Catalog requests by result. |
| `tearenv_engine_service_open_attempts_total` | Service connection attempts by service type and result. |
| `tearenv_engine_service_open_duration_seconds` | Time spent resolving policy, scaling managed workloads, waiting for readiness, and dialing targets. |
| `tearenv_engine_active_connections` | Connections currently proxied through the engine. |
| `tearenv_engine_scale_operations_total` | Scale-up and scale-down operations by result. |
| `tearenv_engine_scale_operation_duration_seconds` | Scaler backend latency. |
| `tearenv_engine_managed_workloads` | Workloads this daemon successfully scaled above zero and hasn't successfully scaled down. |

For example, graph the 95th percentile managed-service startup latency with:

```promql
histogram_quantile(
  0.95,
  sum by (le) (
    rate(tearenv_engine_service_open_duration_seconds_bucket{service_type="managed"}[5m])
  )
)
```

Alert separately on `scale_error`, `dial_error`, and `scaler_unavailable` results. They point to different operator actions: scaler permissions or backend health, target readiness or networking, and missing daemon scaler configuration.

## Add Kubernetes scale-to-zero

For a complete installation, use [the Helm or Kustomize deployment guide](kubernetes-deployment.md). The commands below explain the underlying scaler configuration.

The Kubernetes backend uses in-cluster service-account credentials. It supports lowercase `deployment` and `statefulset` workload kinds and changes only each object's scale subresource.

Create the namespace used by the supplied RBAC template, then apply it:

```sh
kubectl create namespace tearenv-system
kubectl apply -f deploy/kubernetes/rbac.yaml
```

The template creates a `tearenvd` service account in `tearenv-system` and a cluster-wide role for `get` and `update` on `deployments/scale` and `statefulsets/scale`. Bind a namespace-scoped Role instead if all managed workloads live in a known namespace.

Run the pod with `serviceAccountName: tearenvd` and start the daemon with:

```sh
tearenvd serve \
  --listen :2222 \
  --users /var/lib/tearenv/users.json \
  --host-key /var/lib/tearenv/ssh_host_ed25519_key \
  --scaler kubernetes
```

The deprecated `--kubernetes` flag is equivalent to `--scaler kubernetes`. Don't set it together with a different scaler value.

Grant a managed StatefulSet:

```sh
tearenvd service grant \
  --users /var/lib/tearenv/users.json \
  --identity alice \
  --name postgres \
  --target postgres.dev-alice.svc.cluster.local:5432 \
  --local-port 5432 \
  --workload-kind statefulset \
  --workload-namespace dev-alice \
  --workload-name postgres \
  --replicas 1 \
  --ready-timeout 2m \
  --idle-timeout 10m
```

The namespace and workload name identify the scale subresource. The target remains the network endpoint that must accept TCP. tearenv doesn't inspect Kubernetes readiness conditions; after scaling, it attempts a one-second TCP dial every 500 milliseconds until `ready-timeout`.

If multiple aliases belong to the same workload, give them the same workload kind, namespace, and name. tearenvd then shares active connection tracking and won't downscale until all aliases are idle.

Static grants can coexist with scaled grants. Starting without `--scaler` keeps static services working, but every grant with workload metadata fails when a developer tries to connect.

## Use writable persistent state in Kubernetes

Mount the credential file from protected writable storage if developers will enroll through this gateway or administrators will update the file in place. A Kubernetes Secret volume is read-only, so invite authentication may succeed but enrollment can't consume the invite and write the personal token hash.

A Secret volume is suitable only for a pre-provisioned, read-only credential document where enrollment and in-place administration are intentionally disabled. The Kind end-to-end fixture uses that model with precomputed test-token hashes; it isn't an onboarding pattern for production.

Use a single writer for the credential file. Administrative commands use an atomic temporary-file rename, but two commands mutating separate copies or writing concurrently can still overwrite each other's changes. Back up the file before manual maintenance.

## Operate the gateway safely

Watch structured text logs for authentication rejection, service denial, scale operations, readiness failures, and downscale failures. Logs include identities, aliases, targets, and remote addresses, so treat log access as operationally sensitive.

Probe the SSH TCP port for process readiness. A TCP probe confirms that the listener accepts connections; it doesn't validate credential storage, target reachability, or scaler permissions. Add separate synthetic checks for important services.

The current process keeps lifecycle state in memory and doesn't coordinate scale timers across replicas. Run a single active replica for a workload set. If you need high availability, partition workloads so only one gateway manages each workload, or add external lifecycle coordination before running active-active replicas.

Review [the security model](security.md) and [troubleshooting checks](troubleshooting.md) before exposing the gateway.
