# Deploy Dex or connect an existing OIDC provider

Choose one OIDC provider path for each gateway. `bundledDex` installs the
official Dex Helm chart with tearenv. `external` points tearenv at an existing
Dex, Keycloak, or standards-compatible provider. Both paths use the same
authorization-code and SSH-certificate flow, so moving between deployment
models doesn't add a second authentication method.

OIDC mode accepts registrations immediately after authentication. Dex's login
screen and an upstream provider's consent screen don't add a tearenv approval
queue.

## Prepare DNS, TLS, and the SSH user CA

OIDC needs two HTTPS endpoints that developer machines can resolve:

```text
https://auth.example.com         Dex or the external provider
https://tearenv-api.example.com  Tearenv discovery, registration, and exchange
```

The issuer URL must also resolve from the tearenv pod. Split-horizon DNS is
fine, but the internal and external routes must present the same issuer URL and
a trusted certificate.

Create the SSH user CA before installing OIDC mode:

```sh
umask 077
ssh-keygen -t ed25519 -N '' -C tearenv-user-ca -f ./tearenv_user_ca

kubectl create namespace tearenv-system --dry-run=client -o yaml | kubectl apply -f -
kubectl create secret generic tearenv-ssh-user-ca \
  --namespace tearenv-system \
  --from-file=ca_key=./tearenv_user_ca
```

Move the private CA key into the team's secret manager after creating the
Secret. Back up the key separately from the tearenv state volume. Anyone who
can use this key can mint SSH access for every tearenv identity.

## Deploy bundled Dex with GitHub

Create a GitHub OAuth application. Set its callback URL to the Dex issuer plus
`/callback`:

```text
https://auth.example.com/callback
```

Put the GitHub client ID and secret in an owner-only environment file:

```text
GITHUB_CLIENT_ID=replace-me
GITHUB_CLIENT_SECRET=replace-me
```

Create the connector Secret:

```sh
chmod 600 ./dex-github.env
kubectl create secret generic tearenv-dex-github \
  --namespace tearenv-system \
  --from-env-file=./dex-github.env
```

Copy the bundled example and replace both example hostnames, the Ingress class,
and cert-manager annotation:

```sh
cp deploy/helm/tearenv/examples/dex-github-values.yaml ./tearenv-dex-values.yaml
helm dependency build deploy/helm/tearenv
helm upgrade --install tearenv deploy/helm/tearenv \
  --namespace tearenv-system \
  --create-namespace \
  --values ./tearenv-dex-values.yaml \
  --wait \
  --timeout 10m
```

The bundled public client is named `tearenv-cli`. It has no client secret and
allows Dex's loopback redirects for native clients. The chart validates that
`registration.oidc.clientID` matches a public Dex static client.

The default bundled storage uses Dex's Kubernetes storage backend. This avoids
adding a database, but it creates Dex CRDs and cluster-scoped RBAC. Back up the
Dex custom resources and test restore procedures. For larger installations,
configure Dex with a dedicated TLS-protected PostgreSQL database and source its
credentials from a Secret instead of Helm values.

## Use Google instead of GitHub

Create a Google OAuth client with this authorized redirect URI:

```text
https://auth.example.com/callback
```

Change the connector and Secret names in the bundled values file:

```yaml
dex:
  config:
    connectors:
      - type: google
        id: google
        name: Google
        config:
          clientID: $GOOGLE_CLIENT_ID
          clientSecret: $GOOGLE_CLIENT_SECRET
          redirectURI: https://auth.example.com/callback
          hostedDomains:
            - example.com
  envFrom:
    - secretRef:
        name: tearenv-dex-google
```

Create `tearenv-dex-google` from an owner-only environment file containing
`GOOGLE_CLIENT_ID` and `GOOGLE_CLIENT_SECRET`. Remove `hostedDomains` when any
Google account may authenticate. Google group claims need additional Dex and
Google service-account configuration; tearenv doesn't require groups for the
current identity-bound flow.

## Connect an existing Dex installation

Set `registration.oidc.provider=external` and `dex.enabled=false`. Register a
public client in Dex:

```yaml
staticClients:
  - id: tearenv-cli
    name: Tearenv CLI
    public: true
```

When a Dex public client has no explicit redirect URI list, Dex accepts native
loopback redirects. Tearenv opens an ephemeral listener under
`http://127.0.0.1:<port>/callback` and uses PKCE S256, state, and nonce.

Use the external values shape:

```yaml
registration:
  authMode: oidc
  oidc:
    provider: external
    issuerURL: https://dex.example.com
    clientID: tearenv-cli
    audience: tearenv
    subjectTokenType: id-token
    sshUserCA:
      existingSecret: tearenv-ssh-user-ca

dex:
  enabled: false
```

## Connect an existing Keycloak installation

Create a public OpenID Connect client in the target realm with client ID
`tearenv-cli`. Enable the standard authorization-code flow, require PKCE S256,
and set the special native-client redirect URI to `http://127.0.0.1`. Keycloak
treats that exact URI as allowing a random loopback port. Confirm that the ID
token contains `preferred_username`, or select another stable identity claim.

Start from the checked-in example:

```sh
cp deploy/helm/tearenv/examples/external-keycloak-values.yaml ./tearenv-keycloak-values.yaml
helm upgrade --install tearenv deploy/helm/tearenv \
  --namespace tearenv-system \
  --create-namespace \
  --values ./tearenv-keycloak-values.yaml \
  --wait \
  --timeout 10m
```

The Keycloak issuer includes the realm path, for example
`https://keycloak.example.com/realms/developers`. Don't use the Keycloak base
URL as the issuer.

## Trust a privately signed issuer

Store the public CA bundle in a Secret:

```sh
kubectl create secret generic tearenv-oidc-ca \
  --namespace tearenv-system \
  --from-file=ca.crt=./organization-root-ca.pem
```

Reference it from OIDC values:

```yaml
registration:
  oidc:
    ca:
      existingSecret: tearenv-oidc-ca
      secretKey: ca.crt
```

This bundle extends the gateway's system trust for discovery and JWKS requests.
Developer machines must also trust the issuer through their operating-system
certificate store. Don't distribute a private CA key.

## Verify login and certificate exchange

Check both deployments and their HTTPS endpoints:

```sh
kubectl get deployments,services,ingresses --namespace tearenv-system
curl --fail https://auth.example.com/.well-known/openid-configuration
curl --fail https://tearenv-api.example.com/.well-known/tearenv-configuration
```

The tearenv discovery response should report `mode: oidc` and
`subjectTokenType: urn:ietf:params:oauth:token-type:id_token`.

Run a login from a developer machine:

```sh
tearenv login \
  --api-url https://tearenv-api.example.com \
  --server gateway.example.com:2222
```

Then run `tearenv services`. Each OIDC command obtains a fresh short-lived SSH
certificate; OIDC tokens and certificates aren't written to the profile.

## Back up and upgrade Dex separately

The Dex release is version-locked in the tearenv chart's `Chart.lock`. Review
Dex release notes before changing that lock. Back up Dex storage, the connector
Secrets, the SSH user CA, and the tearenv PVC before an upgrade. A Dex outage
blocks new logins and certificates, but it doesn't extend or revoke certificates
that have already been issued.

Changing the issuer URL changes the immutable OIDC owner recorded on every
registration. Plan a migration instead of editing the URL in place: preserve
the old owner mapping or remove and recreate registrations after verifying the
new issuer and subject values. A mismatched existing owner is rejected with
`403 Forbidden`.

## Keep the future operator API provider-neutral

A future controller can express the same choice without making Dex part of the
core Tearenv API:

```yaml
spec:
  authentication:
    type: OIDC
    oidc:
      provider: ManagedDex # or External
      issuerURL: https://auth.example.com
      clientID: tearenv-cli
      audience: tearenv
      subjectTokenType: IDToken
      sshUserCASecretRef:
        name: tearenv-ssh-user-ca
```

This document describes a future API shape, not an installed CRD or controller.
The current Helm values remain the executable deployment contract.

For provider-specific options, use the official
[Dex connector documentation](https://dexidp.io/docs/connectors/),
[Dex storage documentation](https://dexidp.io/docs/configuration/storage/), and
[Keycloak OIDC documentation](https://www.keycloak.org/securing-apps/oidc-layers).
