# Authenticate and register

tearenv supports exactly one registration and SSH authentication mode per
gateway: a shared registration token or OIDC. The server advertises the active
mode at `GET /.well-known/tearenv-configuration`, so the same `tearenv login`
command works with either one.

In both modes, login creates or reuses a local Ed25519 key and submits only its
public key as a `UserRegistration`. The private key never leaves the developer
machine.

## Log in

Run:

```sh
tearenv login \
  --api-url https://tearenv-api.example.com \
  --namespace default \
  --server gateway.example.com:2222
```

With token authentication, also pass the operator-provided token:

```sh
tearenv login \
  --api-url https://tearenv-api.example.com \
  --registration-token-file ./tearenv-registration-token \
  --server gateway.example.com:2222
```

With OIDC, tearenv opens the provider in your browser and uses authorization
code flow with PKCE, state, and nonce validation. If the operator enabled device
authorization, pass `--oidc-device` instead. Your tearenv identity comes from
the configured ID-token claim; `--identity` can only assert the same value.

On Linux, the default files are:

```text
~/.config/tearenv/id_ed25519
~/.config/tearenv/user-registration.yaml
~/.config/tearenv/config.json
```

The files use mode `0600`, and the directory uses mode `0700`. OIDC subject
tokens, refresh tokens, and SSH certificates aren't saved.

## Understand the OIDC-to-SSH exchange

The server advertises whether the client should submit an ID token or an RFC
9068 access token. ID tokens are the default and work with Dex, Keycloak, and
most interactive OIDC providers. Access-token mode is available for providers
that issue audience-bound JWT access tokens with `typ=at+jwt`.

After registration, the client makes an RFC 8693 token-exchange request to
`POST /oauth/token` for the `ssh:connect` scope. The request names one already
accepted key. tearenvd checks all of the following before issuing a certificate:

- the JWT signature, issuer, audience, expiry, and configured subject-token type;
- the subject that owns the registration and the claim-derived identity;
- the requested registration namespace and public-key name;
- the requested token type, audience, and `ssh:connect` scope.

The returned SSH user certificate is short-lived and valid only for that
identity. The client combines it with the local private key in memory. The SSH
gateway trusts only its configured user CA in OIDC mode and closes existing SSH
connections when the certificate expires.

The default identity claim is `preferred_username`. Choose a stable, unique,
human-readable claim that also matches
`^[A-Za-z0-9][A-Za-z0-9._@-]{0,63}$`. The OIDC `sub` claim remains the immutable
owner even if the display identity changes.

Use [the OIDC provider deployment guide](oidc-providers.md) to deploy the
optional Dex dependency or connect an existing Dex or Keycloak installation.

## Understand token mode

Token mode protects registration with one shared bearer token, then authenticates
SSH directly with the accepted registered public key. The token isn't written to
the client profile. `TEARENV_REGISTRATION_TOKEN` is available for automation,
but a protected file avoids putting the secret in shell history.

The shared token authorizes registration but doesn't bind a caller to an
identity. The first writer can claim an unregistered identity. Use OIDC when the
gateway crosses a trusted-team boundary.

## Submit the resource API directly

Login sends an idempotent JSON `PUT` request:

```text
PUT /apis/tearenv.io/v1alpha1/namespaces/{namespace}/userregistrations/{name}
```

The built-in API accepts valid registrations immediately. Repeating the same
request returns the stored object, including its server-owned UID and resource
version. Changing the spec at the same path returns `409 Conflict`.

In OIDC mode, `status.authenticatedPrincipal` records only the verified issuer
and subject. A matching legacy registration without owner status is adopted on
the first authenticated, byte-for-byte identical submission. A different
subject gets `403 Forbidden`.

Use TLS whenever the API is reachable over a network. The built-in listener is
HTTP, so terminate TLS at a trusted Ingress or reverse proxy. An unauthenticated
API is allowed only on a loopback listen address.

Check [the versioned API contract](../api/v1alpha1/README.md) for resource fields
and status semantics.
