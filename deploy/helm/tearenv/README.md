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

## Enable external OIDC

Create a dedicated SSH user CA and store it in a Secret. Don't put the private
key in values or source control:

```sh
ssh-keygen -t ed25519 -N '' -C tearenv-user-ca -f ./tearenv_user_ca
kubectl -n tearenv-system create secret generic tearenv-ssh-user-ca \
  --from-file=ca_key=./tearenv_user_ca
```

Set the OIDC values:

```yaml
registration:
  authMode: oidc
  oidc:
    provider: external
    issuerURL: https://id.example.com
    clientID: tearenv-cli
    audience: tearenv
    identityClaim: preferred_username
    subjectTokenType: id-token
    scopes: [openid, profile]
    deviceFlow: false
    sshUserCA:
      existingSecret: tearenv-ssh-user-ca
      secretKey: ca_key
      certificateTTL: 10m
      clockSkew: 30s
  ingress:
    enabled: true
    host: tearenv-api.example.com
    tls:
      enabled: true
      secretName: tearenv-api-tls
```

The chart rejects incomplete OIDC settings and mounts only the required
Secrets. It doesn't create external identity-provider clients, DNS, or TLS
certificates. Configure those before login, and allow authorization code with
PKCE plus loopback redirect URIs for the public native client. Start from
[`examples/external-keycloak-values.yaml`](examples/external-keycloak-values.yaml)
for Keycloak.

## Deploy bundled Dex

The official Dex chart is an optional dependency. Build dependencies for a
source checkout, then install the bundled example:

```sh
helm dependency build deploy/helm/tearenv
helm upgrade --install tearenv deploy/helm/tearenv \
  --namespace tearenv-system \
  --create-namespace \
  --values deploy/helm/tearenv/examples/dex-github-values.yaml
```

Bundled mode requires `registration.oidc.provider=bundledDex`,
`dex.enabled=true`, an HTTPS `dex.config.issuer`, and a matching public static
client. Connector credentials come from an existing Secret through
`dex.envFrom`; don't put them in values. The default Kubernetes storage backend
creates Dex CRDs and cluster-scoped RBAC.

Prometheus metrics are available from `/metrics` on the separate `<release>-metrics` Service. Set `metrics.enabled=false` to remove that listener and Service.

To create one environment namespace per authenticated identity, put a reviewed `EnvironmentBlueprint` in a ConfigMap and install with `blueprint.enabled=true` and `blueprint.existingConfigMap=NAME`. The chart mounts the file and grants cluster-wide create and patch access for common namespaced blueprint resources. Check [the environment blueprint guide](../../../docs/environment-blueprints.md) before enabling these permissions.

See [the Kubernetes deployment guide](../../../docs/kubernetes-deployment.md)
for installation and [the authentication guide](../../../docs/authentication.md)
for provider requirements, token exchange, migration, and rotation.
Use [the OIDC provider guide](../../../docs/oidc-providers.md) for bundled Dex,
GitHub, Google, existing Dex, and Keycloak setup.
