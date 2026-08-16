# Install tearenv with Helm

The chart runs one `tearenvd` gateway and keeps its policy, SSH host key, and user registrations on a persistent volume. It creates separate Services for SSH, registration, and metrics so publishing SSH doesn't accidentally publish the HTTP endpoints.

Create a namespace and a bootstrap policy Secret:

```sh
kubectl create namespace tearenv-system
kubectl create secret generic tearenv-bootstrap \
  --namespace tearenv-system \
  --from-file=users.json=/path/to/users.json
```

Install a released chart from GHCR:

```sh
helm upgrade --install tearenv oci://ghcr.io/fr0stylo/charts/tearenv \
  --version 0.2.0 \
  --namespace tearenv-system \
  --create-namespace \
  --set bootstrap.existingSecret=tearenv-bootstrap
```

For a source checkout, replace the OCI URL with `./deploy/helm/tearenv` and set an immutable `image.tag`.

Start from [`examples/production-values.yaml`](examples/production-values.yaml) when preparing an installation-specific values file.

Set `service.type=LoadBalancer` to publish only the SSH endpoint. The registration API stays cluster-local unless `registration.ingress.enabled=true`; the Ingress requires TLS by default. Helm generates and preserves a registration bearer token unless `registration.token.existingSecret` names a Secret managed by your secret system.

Prometheus metrics are available from `/metrics` on the separate `<release>-metrics` Service. Set `metrics.enabled=false` to remove that listener and Service.

To create one environment namespace per authenticated identity, put a reviewed `EnvironmentBlueprint` in a ConfigMap and install with `blueprint.enabled=true` and `blueprint.existingConfigMap=NAME`. The chart mounts the file and grants cluster-wide create and patch access for common namespaced blueprint resources. Check [the environment blueprint guide](../../../docs/environment-blueprints.md) before enabling these permissions.

See [the Kubernetes deployment guide](../../../docs/kubernetes-deployment.md) for policy setup, public-key authentication, upgrades, and troubleshooting.
