# Deploy tearenv on Kubernetes

> The current Helm and Kustomize manifests predate the built-in registration API. They don't yet expose port `8080` or mount persistent registration storage. Adapt those two pieces before using the new login flow in Kubernetes.

Use either Helm or Kustomize to run one `tearenvd` gateway with persistent policy and SSH host-key storage. Both setups use a namespaced scaler Role by default and run the containers without root privileges.

## Publish the gateway image from a version tag

Push a semantic version tag to let the release workflow build the `linux/amd64` and `linux/arm64` image and publish it to GitHub Container Registry:

```sh
git tag v0.1.0
git push origin v0.1.0
```

The workflow publishes `ghcr.io/<owner>/<repository>:0.1.0` and the matching major, minor, exact Git tag, and `latest` aliases. It listens for the Git tag push, not the GitHub `release` event.

The image contains `tearenvd` and runs as user `65532`.

## Create the initial policy

Create a local policy with a service grant:

```sh
make build

./bin/tearenvd service grant \
  --users /tmp/tearenv-users.json \
  --identity alice \
  --name postgres \
  --target postgres.tearenv-system.svc.cluster.local:5432
```

Keep the policy file private because it contains service targets and authorization rules.

## Install with Helm

Create the bootstrap Secret and install the chart:

```sh
kubectl create namespace tearenv-system
kubectl create secret generic tearenv-bootstrap \
  --namespace tearenv-system \
  --from-file=users.json=/tmp/tearenv-users.json

helm upgrade --install tearenv deploy/helm/tearenv \
  --namespace tearenv-system \
  --set image.repository=registry.example.com/tearenv \
  --set image.tag=0.1.0 \
  --set bootstrap.existingSecret=tearenv-bootstrap
```

The default Service is cluster-local. Set `service.type=LoadBalancer` in a values file when the cluster should publish the SSH endpoint.

The Service also exposes the Prometheus endpoint as the `metrics` port. Set `metrics.enabled=false` to disable it, or change `metrics.port` when port `9090` isn't available. Point your Prometheus discovery configuration or ServiceMonitor at the `metrics` port and `/metrics` path.

The scaler Role covers only the release namespace. List workload namespaces with `scaler.rbac.namespaces`, or use `scaler.rbac.clusterWide=true` only when cluster-wide scaling is required.

## Install with Kustomize

Copy `/tmp/tearenv-users.json` over `deploy/kustomize/overlays/default/users.json`, then set the published image in that overlay's `kustomization.yaml`. Don't commit the populated policy file.

Preview and apply the default setup:

```sh
kubectl kustomize deploy/kustomize/overlays/default
kubectl apply -k deploy/kustomize/overlays/default
```

Choose another included overlay when needed:

```sh
kubectl apply -k deploy/kustomize/overlays/load-balancer
kubectl apply -k deploy/kustomize/overlays/cluster-wide
kubectl apply -k deploy/kustomize/overlays/blueprint
```

The default and load-balancer overlays can scale workloads only in `tearenv-system`. The cluster-wide overlay replaces that Role with a scaler ClusterRole. The blueprint overlay mounts a starter definition and adds the broader ClusterRole needed to create identity namespaces and their resources.

The Kustomize Service exposes `/metrics` on its named `metrics` port, `9090`.

## Expose the registration API

Provide a persistent directory to `tearenvd --registrations`, expose its `--api-listen` port through TLS, then register from the developer client:

```sh
tearenv login \
  --api-url https://tearenv-api.example.com \
  --identity alice \
  --server gateway.example.com:2222
```

The current API accepts valid registrations by default. Restrict which network can reach it until caller identity enforcement is added.

## Create one environment namespace per identity

Generate and review a reusable environment definition with:

```sh
tearenvd blueprint init \
  --name web-development \
  > web-development.yaml
```

Review and store the file in a team-controlled configuration repository. The generated namespace template separates environments by authenticated identity and blueprint name. Resource templates omit `metadata.namespace` because `tearenvd` injects the rendered namespace.

For Helm, create a ConfigMap from the file and enable it during installation:

```sh
kubectl create configmap tearenv-blueprint \
  --namespace tearenv-system \
  --from-file=environment-blueprint.yaml=web-development.yaml

helm upgrade --install tearenv deploy/helm/tearenv \
  --namespace tearenv-system \
  --reuse-values \
  --set blueprint.enabled=true \
  --set blueprint.existingConfigMap=tearenv-blueprint
```

For Kustomize, replace `deploy/kustomize/overlays/blueprint/environment-blueprint.yaml` with the reviewed file, then run:

```sh
kubectl apply -k deploy/kustomize/overlays/blueprint
```

An accepted public-key login creates or patches its identity namespace and resources. Login fails if reconciliation fails. Repeat logins use server-side apply against the same objects. Removing an object from the blueprint doesn't delete the existing object.

Blueprint provisioning requires cluster-wide permission to create namespaces and create or patch the namespaced APIs used by the document. The supplied Helm and Kustomize rules cover common core, `apps`, `batch`, and `networking.k8s.io` resources. Review these permissions and extend them only for approved resource types. Don't apply the blueprint file directly with `kubectl`; `EnvironmentBlueprint` isn't a Kubernetes CRD.

## Operate and upgrade the gateway

Wait for startup and inspect logs:

```sh
kubectl rollout status deployment/tearenvd -n tearenv-system
kubectl logs deployment/tearenvd -n tearenv-system
```

Helm names the Deployment from its release; use `kubectl get deployment -n tearenv-system` if the name differs.

The bootstrap Secret is copied only when the persistent volume has no `users.json`. Later Secret changes don't replace live state. Run administrative commands against the mounted file or use a controlled maintenance job, and back up both `users.json` and `ssh_host_ed25519_key` from the state volume.

The Helm chart keeps its generated PVC after uninstall by default. Kustomize doesn't add a retention policy, so check the PVC before deleting resources.

Keep `replicaCount` at `1`. Gateway connection counters and scale-down timers aren't coordinated across replicas.

If the pod stays in `Init`, confirm that the bootstrap Secret and key exist. If it stays `Pending`, inspect the PVC and storage class. For scaler errors, verify the grant's workload namespace and run `kubectl auth can-i update deployments/scale --as=system:serviceaccount:tearenv-system:tearenvd -n WORKLOAD_NAMESPACE`.

See [the authentication guide](authentication.md), [operator guide](operator-guide.md), and [troubleshooting guide](troubleshooting.md) for the underlying configuration and security model.
