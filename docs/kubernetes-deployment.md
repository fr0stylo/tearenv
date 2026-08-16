# Deploy tearenv on Kubernetes

Use the Helm chart for the supported production installation. The chart runs one gateway replica, stores its policy, SSH host key, and registrations on a persistent volume, and publishes SSH separately from the registration and metrics endpoints.

The current storage and scale lifecycle require one active replica. Don't increase `replicaCount`: multiple gateways don't coordinate connection counts or writes.

## Install the required tools

The operator workstation needs `kubectl`, Helm, and the `tearenvd` binary from the same release you plan to deploy. Download the Linux or macOS `tearenv_<version>_<os>_<architecture>.tar.gz` archive and `checksums.txt` from the GitHub release, verify the checksum, and put `tearenvd` on your `PATH`.

The cluster needs:

- A default StorageClass or an existing ReadWriteOnce PVC.
- A way to publish TCP port `2222`, such as a LoadBalancer Service.
- An Ingress controller and TLS certificate only when developers must register without an operator-managed port-forward.

## Create the initial service policy

Create a policy outside the cluster before installing the chart:

```sh
tearenvd service grant \
  --users ./users.json \
  --identity alice \
  --name postgres \
  --target postgres.development.svc.cluster.local:5432 \
  --local-port 5432 \
  --workload-kind statefulset \
  --workload-namespace development \
  --workload-name postgres \
  --replicas 1 \
  --ready-timeout 2m \
  --idle-timeout 10m
```

The identity in the policy must match the identity the developer enters during `tearenv login`.

Store the policy in a Secret instead of Helm values:

```sh
kubectl create namespace tearenv-system

kubectl create secret generic tearenv-bootstrap \
  --namespace tearenv-system \
  --from-file=users.json=./users.json
```

The bootstrap Secret initializes a new volume. Changing the Secret later does not overwrite the live policy.

## Let Helm manage an enrollment token

By default, Helm generates a 48-character registration token and preserves its Secret across upgrades. This works well for an interactive Helm installation.

For GitOps, create the Secret through your existing secret manager and configure its name:

```sh
openssl rand -base64 36 > tearenv-registration-token
chmod 600 tearenv-registration-token

kubectl create secret generic tearenv-registration \
  --namespace tearenv-system \
  --from-file=token=./tearenv-registration-token
```

Don't commit the token or put it in Helm values. Anyone holding it can submit a registration for an unclaimed identity. Registrations are still accepted automatically; the token protects access to enrollment rather than adding an approval step.

## Install the chart with immutable versions

Copy `deploy/helm/tearenv/examples/production-values.yaml` to `tearenv-values.yaml`, then set the release tag, Secret names, workload namespaces, and Service settings. The essential values are:

```yaml
image:
  tag: "0.2.0"

bootstrap:
  existingSecret: tearenv-bootstrap

registration:
  token:
    existingSecret: tearenv-registration

service:
  type: LoadBalancer

scaler:
  rbac:
    namespaces:
      - development
```

Install the matching chart version:

```sh
helm upgrade --install tearenv oci://ghcr.io/fr0stylo/charts/tearenv \
  --version 0.2.0 \
  --namespace tearenv-system \
  --create-namespace \
  --values tearenv-values.yaml \
  --wait \
  --timeout 5m
```

For a source checkout, replace the OCI URL with `./deploy/helm/tearenv`.

Confirm the pod, PVC, and three Services:

```sh
kubectl get pods,pvc,services --namespace tearenv-system
kubectl rollout status deployment/tearenv --namespace tearenv-system
```

The main Service contains only SSH. `<release>-registration` and `<release>-metrics` remain cluster-local.

## Onboard through a port-forward

Keeping registration cluster-local is the safest default. An operator can open a temporary port-forward:

```sh
kubectl port-forward \
  --namespace tearenv-system \
  service/tearenv-registration 8080:8080
```

Retrieve a Helm-generated token into an owner-only local file when you didn't provide an existing Secret:

```sh
umask 077
kubectl get secret tearenv-registration \
  --namespace tearenv-system \
  --output jsonpath='{.data.token}' \
  | base64 --decode > tearenv-registration-token
```

Give the token file to the intended developer through an approved secret-sharing channel. The developer runs:

```sh
tearenv login \
  --api-url http://127.0.0.1:8080 \
  --registration-token-file ./tearenv-registration-token \
  --identity alice \
  --server gateway.example.com:2222
```

The token is used only for registration and isn't stored in the tearenv profile. Ongoing connections authenticate with the generated SSH key.

## Expose registration through TLS

Use a TLS Ingress when developers need permanent self-service registration. Add the Ingress settings to `tearenv-values.yaml`:

```yaml
registration:
  token:
    existingSecret: tearenv-registration
  ingress:
    enabled: true
    className: nginx
    host: tearenv-api.example.com
    annotations:
      cert-manager.io/cluster-issuer: letsencrypt-prod
    tls:
      enabled: true
      secretName: tearenv-registration-tls
```

Upgrade the release, then use `https://tearenv-api.example.com` as `--api-url`. Don't publish the registration Service directly as a LoadBalancer: `tearenvd` serves HTTP, and TLS termination belongs at the Ingress or another trusted reverse proxy.

## Verify the SSH host key before onboarding

Read the gateway fingerprint from its startup log:

```sh
kubectl logs deployment/tearenv \
  --namespace tearenv-system \
  | grep host_key_fingerprint
```

Publish that fingerprint through a trusted internal channel. Developers should compare it with the key at the published endpoint before accepting it. `ssh-keyscan` retrieves a key but doesn't establish trust by itself.

The host key lives on the state PVC and survives pod replacement. Restoring a different PVC changes the fingerprint and requires a deliberate rotation procedure.

## Change grants without replacing the pod

Run the administrative command in the gateway container to update the live policy atomically:

```sh
kubectl exec deployment/tearenv \
  --namespace tearenv-system \
  -- /usr/local/bin/tearenvd service grant \
  --users /var/lib/tearenv/users.json \
  --identity alice \
  --name redis \
  --target redis.development.svc.cluster.local:6379
```

The daemon reloads service policy when a catalog or service request arrives. Back up the PVC before bulk policy changes.

## Keep scaler permissions narrow

List workload namespaces under `scaler.rbac.namespaces`. The chart creates one Role and RoleBinding per namespace with only `get` and `update` on Deployment and StatefulSet scale subresources.

Set `scaler.rbac.clusterWide=true` only when the gateway must manage workloads in namespaces that can't be listed ahead of time. Enabling an environment blueprint also requires broader cluster permissions; review the [environment blueprint guide](environment-blueprints.md) before enabling it.

## Back up and upgrade without changing identity

Take a storage snapshot or backup of the state PVC before every upgrade. The volume contains:

- `users.json`, including service grants and private targets.
- `ssh_host_ed25519_key`, which preserves server identity.
- `registrations/`, which contains accepted public keys.

Also back up an externally managed registration-token Secret. Protect backups as authentication and network-policy data.

Upgrade with matching immutable image and chart versions:

```sh
helm upgrade tearenv oci://ghcr.io/fr0stylo/charts/tearenv \
  --version 0.2.1 \
  --namespace tearenv-system \
  --values tearenv-values.yaml \
  --wait \
  --timeout 5m
```

The Deployment uses `Recreate` because one ReadWriteOnce volume and one writer own the state. Expect a short connection outage during an upgrade. Roll back the chart and restore the PVC snapshot together if a release changes persisted data incompatibly.

## Monitor the gateway

Prometheus metrics are available at `/metrics` on the cluster-local `tearenv-metrics` Service. Scrape at least:

- `tearenv_daemon_ready`
- `tearenv_ssh_authentication_attempts_total`
- `tearenv_ssh_handshake_failures_total`
- `tearenv_engine_service_open_attempts_total`
- `tearenv_engine_scale_operations_total`
- `tearenv_engine_managed_workloads`

Alert on readiness loss, increasing authentication errors, scale errors, and targets that exceed their startup timeout. Logs contain identities, aliases, private targets, and remote addresses, so restrict log access.

## Uninstall without deleting state accidentally

The chart marks the generated PVC and registration-token Secret with Helm's keep policy. `helm uninstall` removes the workload but leaves those resources behind.

Before deleting either resource, confirm that you have a usable backup and intend to remove the gateway identity and registrations. A new PVC creates a new SSH host key.

Use the [troubleshooting guide](troubleshooting.md) for startup, registration, authentication, RBAC, and scaling failures.
