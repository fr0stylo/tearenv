# Install tearenv with Helm

The chart runs one `tearenvd` gateway, keeps its policy and SSH host key on a persistent volume, and grants namespaced scale access by default.

Create a namespace and a bootstrap policy Secret:

```sh
kubectl create namespace tearenv-system
kubectl create secret generic tearenv-bootstrap \
  --namespace tearenv-system \
  --from-file=users.json=/path/to/users.json
```

Install the chart with an image you have published:

```sh
helm upgrade --install tearenv ./deploy/helm/tearenv \
  --namespace tearenv-system \
  --set image.repository=registry.example.com/tearenv \
  --set image.tag=0.1.0 \
  --set bootstrap.existingSecret=tearenv-bootstrap
```

Set `service.type=LoadBalancer` to publish the SSH endpoint. Use `scaler.rbac.namespaces` for a known set of workload namespaces, or set `scaler.rbac.clusterWide=true` when the gateway must manage workloads throughout the cluster.

Prometheus metrics are available from `/metrics` on the Service's `metrics` port. Set `metrics.enabled=false` to remove the metrics listener and Service port, or set `metrics.port` to change the port.

To create one environment namespace per authenticated identity, put a reviewed `EnvironmentBlueprint` in a ConfigMap and install with `blueprint.enabled=true` and `blueprint.existingConfigMap=NAME`. The chart mounts the file and grants cluster-wide create and patch access for common namespaced blueprint resources. Check [the environment blueprint guide](../../../docs/environment-blueprints.md) before enabling these permissions.

See [the Kubernetes deployment guide](../../../docs/kubernetes-deployment.md) for policy setup, public-key authentication, upgrades, and troubleshooting.
