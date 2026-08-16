# Register with one SSH key

tearenv uses one developer authentication flow: `tearenv login` creates or reuses a local Ed25519 key and submits its public key as a `UserRegistration` resource. The private key never leaves the developer machine.

Run:

```sh
tearenv login \
  --api-url https://tearenv-api.example.com \
  --registration-token-file ./tearenv-registration-token \
  --namespace default \
  --server gateway.example.com:2222
```

The command prompts for an identity and offers the local hostname as the default. Pass `--identity` to skip the prompt.

On Linux, the default files are:

```text
~/.config/tearenv/id_ed25519
~/.config/tearenv/user-registration.yaml
~/.config/tearenv/config.json
```

The key, registration document, and profile use mode `0600`. The directory uses mode `0700`.

## Submit the registration through the resource API

Login sends JSON with an idempotent `PUT` request:

```text
PUT /apis/tearenv.io/v1alpha1/namespaces/{namespace}/userregistrations/{name}
```

For example:

```text
PUT /apis/tearenv.io/v1alpha1/namespaces/default/userregistrations/alice
```

The API owns the durable resource. The built-in tearenvd API accepts valid registrations immediately and returns the stored `UserRegistration` with `Accepted=True`. Login then writes `config.json`. The enrollment token is sent as an HTTP bearer credential and isn't saved in that profile.

The first request creates the resource. Repeating the same request returns it unchanged, including its server-owned UID and resource version. A request that changes the spec at the same path returns `409 Conflict`; key replacement is deliberately not part of this first version.

`tearenvd serve` listens on `127.0.0.1:8080` by default and stores registrations under `.data/registrations`. Set `--registration-token-file` whenever the API is reachable by another machine. Use `--api-listen :8080` with TLS termination at a trusted reverse proxy; the built-in listener serves HTTP.

Pass the operator-provided token to `tearenv login` with `--registration-token-file`. `TEARENV_REGISTRATION_TOKEN` is available for automation, but a protected file avoids putting a secret into shell history.

## Authorize identity claims at the API boundary

Possessing a newly generated private key proves control of that key during SSH authentication. It doesn't prove that the person is entitled to the identity in `spec.identity`.

The current self-service policy accepts people by default. The first writer can therefore claim an identity that has service grants. The enrollment token restricts who can submit registrations, but it doesn't bind a token holder to a specific identity. Keep it in a trusted onboarding channel and rotate it after disclosure.

## Keep the private key local

Anyone who can read `id_ed25519` can authenticate as its accepted identity. Don't copy the private key into the registration API, Kubernetes, container images, tickets, or shared profiles.

The submitted resource contains public material only. Check [the versioned API contract](../api/v1alpha1/README.md) for its fields and status semantics.
