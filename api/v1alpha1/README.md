# Define a user registration resource

The `v1alpha1` package defines an experimental, Kubernetes-style `UserRegistration` API. It follows the `apiVersion`, `kind`, `metadata`, `spec`, and optional `status` resource shape used by Grafana's resource APIs.

The type package is an API contract, not a Kubernetes CRD. `tearenvd` serves a small file-backed implementation of the contract, but it doesn't install a CRD or run a controller.

## Declare public credentials only

Start with [the example registration](examples/user-registration.yaml):

```yaml
apiVersion: tearenv.io/v1alpha1
kind: UserRegistration
metadata:
  name: alice
  namespace: default
spec:
  identity: alice
  publicKeys:
    - name: laptop
      key: ssh-ed25519 AAAA... alice@example
```

`metadata.name` is the deterministic API-safe form of `spec.identity`; the validator requires them to match. Each public key has a stable name for future updates and removal. A registration must contain at least one valid OpenSSH public key.

Don't put a private key, personal token, or enrollment invite in this resource. Those values are bearer credentials. The registration API intentionally contains public key material only.

## Load and validate a resource

Consumers can strictly decode YAML or JSON:

```go
registration, err := v1alpha1.LoadUserRegistration(contents)
if err != nil {
    return err
}
```

Strict decoding rejects unknown fields. Validation checks the API envelope, Kubernetes-compatible metadata names, tearenv's identity format, key names, key syntax, and duplicate keys.

## Understand default acceptance

`spec.identity` is a requested identity, not proof that the author owns that identity. The current API accepts the first valid registration at a resource path automatically. This is intentionally a simple self-service policy, not an approval system. Protect the HTTP endpoint when identity claims aren't trusted.

## Store registrations through the resource API

`tearenv login` sends the resource as JSON with an idempotent request:

```text
PUT /apis/tearenv.io/v1alpha1/namespaces/{namespace}/userregistrations/{name}
```

The API response must contain the stored resource. Login writes its local profile only after the response contains this standard condition:

```yaml
status:
  observedGeneration: 1
  authenticatedPrincipal:
    method: oidc
    issuer: https://id.example.com
    subject: 00u123456789
  conditions:
    - type: Accepted
      status: "True"
      reason: AcceptedByDefault
```

An identical `PUT` is idempotent and returns the original resource. The stored spec is immutable: changing the identity or public key at the same resource path returns `409 Conflict`. In OIDC mode, `authenticatedPrincipal` is server-owned and binds the resource to the verified issuer and subject. It contains no bearer token.

By default, `tearenvd` stores resources as protected YAML under `.data/registrations/{namespace}/{name}.yaml`. Token mode authenticates SSH public keys directly from accepted resources. OIDC mode uses the accepted key only when exchanging a verified ID token or RFC 9068 access token for a short-lived, CA-signed SSH certificate. The CLI's local copy isn't authoritative.

A future controller can watch the same API shape and reconcile it into another authentication backend without changing the client document.

Because this version is `v1alpha1`, fields and semantics can change before a stable API is published.
