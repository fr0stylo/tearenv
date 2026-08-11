# Choose an authentication method

tearenv separates authentication from authorization. Authentication proves an identity with a token or SSH key. Authorization maps that identity to the service aliases in the policy file.

The gateway can enable token and public-key authentication together. Each connection still uses the same identity-bound service policy.

## Use invites and tokens for standalone deployments

The default flow needs no Kubernetes access:

```sh
INVITE=$(tearenvd invite \
  --users /var/lib/tearenv/users.json \
  --identity alice)

tearenv login \
  --method token \
  --identity alice \
  --server gateway.example.com:2222 \
  --invite "$INVITE"
```

The invite is single-use. The gateway stores a hash of the resulting token, while the developer profile stores the plaintext token with mode `0600`.

## Keep private keys local for Kubernetes login

Kubernetes login creates or reuses a local Ed25519 private key and adds only its public key to a Kubernetes Secret. Don't put the private key in Kubernetes. Anyone who can read that private key can authenticate as its identity.

First, grant the identity at least one service. External authentication identities don't need an invite or token record:

```sh
tearenvd service grant \
  --users /var/lib/tearenv/users.json \
  --identity alice \
  --name postgres \
  --target postgres.dev-alice.svc.cluster.local:5432
```

Verify the gateway host key, then register the developer key:

```sh
tearenv login \
  --method kubernetes \
  --identity alice \
  --server gateway.example.com:2222 \
  --kubernetes-namespace tearenv-system \
  --kubernetes-secret tearenv-authorized-keys
```

The command uses the selected kubeconfig credentials, creates the Secret when needed, preserves keys for other identities, and saves the private-key path in the tearenv profile. Use `--kubeconfig` or `--kubernetes-context` when the default kubectl context isn't the intended cluster.

Mount the Secret data into the gateway pod:

```yaml
volumeMounts:
  - name: authorized-keys
    mountPath: /etc/tearenv/authorized-keys
    readOnly: true
volumes:
  - name: authorized-keys
    secret:
      secretName: tearenv-authorized-keys
      defaultMode: 0444
      items:
        - key: authorized_keys.json
          path: authorized_keys.json
```

Start the gateway with the mounted document:

```sh
tearenvd serve \
  --users /var/lib/tearenv/users.json \
  --host-key /var/lib/tearenv/ssh_host_ed25519_key \
  --authorized-keys /etc/tearenv/authorized-keys/authorized_keys.json
```

The gateway reloads the document for each authentication attempt. Kubernetes projected Secret updates therefore take effect without restarting tearenvd, subject to Kubernetes volume propagation delay.

## Treat Secret write access as identity administration

The Secret contains public keys, but its integrity is security-sensitive. A person who can change the shared document can add a key for another identity and inherit that identity's grants. Ordinary access to a Kubernetes cluster isn't enough; the caller needs explicit permission to get and update this Secret.

Don't grant broad Secret write access just to make login self-service. Use an operator-controlled provisioning workflow, admission policy that binds Kubernetes identity to the tearenv identity, or a future OIDC provider. Kubernetes Secrets are base64-encoded and aren't encrypted at rest unless the cluster enables encryption.

## Add OIDC behind the provider boundary

OIDC isn't implemented yet. The gateway now depends on the `authorization.Authenticator` interface and evaluates a provider chain, so an OIDC verifier can be added without changing service policy or the SSH gateway.

An OIDC implementation still needs explicit choices for issuer URL, audience, username claim and prefix, required claims, token lifetime, refresh behavior, and browser or device login. The provider must validate the signature, issuer, audience, expiry, and required claims, then require the verified identity to match the requested SSH identity. Avoid saving a short-lived ID token as though it were a permanent tearenv token.
