# Diagnose tearenv problems

Start on the side that reports the failure. Client startup failures usually concern the profile, host key, authentication, or local port. Failures after a local application connects usually concern authorization, scaling, DNS, or target readiness.

## Fix login failures

### `invite is required`

Pass `--invite` or set `TEARENV_INVITE` for the `login` process. Confirm the variable isn't empty:

```sh
test -n "$TEARENV_INVITE" && echo "invite is set"
```

### SSH authentication fails or enrollment is rejected

Confirm the identity exactly matches the one passed to `tearenvd invite`. Invites are identity-bound, single-use bearer secrets. If the invite was already redeemed, copied incorrectly, or replaced by a newer invite, create and deliver a new one.

On the gateway, look for `client enrollment rejected` or `client enrollment failed`. A read-only credential path can authenticate an invite but fail when enrollment tries to consume it and persist the token hash. Move the credential file to writable protected storage.

### Host key verification fails

Confirm that the profile's host and port match the known-hosts entry:

```sh
ssh-keygen -F '[gateway.example.com]:2222' -f ~/.ssh/known_hosts
```

Fetch the current key into a separate file and compare its fingerprint through a trusted channel:

```sh
ssh-keyscan -p 2222 gateway.example.com > /tmp/tearenv-current-key
ssh-keygen -lf /tmp/tearenv-current-key
```

Don't delete an old key or use `--insecure-skip-host-key-check` until the operator confirms an intentional rotation. An unexpected change can indicate the wrong endpoint or an interception attempt.

### `load known_hosts` reports a missing file

Create the file after verifying the key, or pass its actual path with `--known-hosts`. In containers and service accounts, `~/.ssh/known_hosts` may not exist.

## Fix profile and authentication failures

### `run 'tearenv login' first`

The default profile doesn't exist or can't be read. Run `tearenv login`, or pass the profile you already created:

```sh
tearenv services --config ~/.config/tearenv/staging.json
```

### Profile permissions are rejected

The profile must have no group or world bits:

```sh
chmod 600 ~/.config/tearenv/config.json
chmod 700 ~/.config/tearenv
```

If a mounted filesystem can't enforce Unix modes, place the profile on a filesystem that can protect it.

### `unable to authenticate` or `client authentication rejected`

Check the saved `server` and `identity`, then ask the operator whether a new invite was redeemed for the same identity. Redeeming it rotates the personal token, so older profiles stop authenticating. Login again with the current invite.

Temporary `--identity`, `--token`, `TEARENV_TOKEN`, and `--server` overrides can also create a mismatched identity/token pair. Remove the overrides and retry with the saved profile.

## Fix service selection and local listeners

### `service ... is not granted to this identity`

Run `tearenv services` and use the exact alias. Service names are lowercase and case-sensitive. If the expected alias is absent, the operator should check the identity and grant file.

### `no services are granted to this identity`

The authentication succeeded, but the catalog is empty. Ask the operator to grant at least one service to the same identity. Grants for another identity don't appear.

### `address already in use`

Another process owns the suggested local port. Find it with the platform's socket tool, for example:

```sh
ss -ltnp | grep ':5432'
```

Stop that process or override the local address:

```sh
tearenv connect postgres=127.0.0.1:15432
```

If multiple selected services have the same suggested port, override at least one of them.

### `service ready` appears, but the application fails

`service ready` means only that the local listener and gateway SSH connection are ready. Watch the next client and server log entries while opening an application connection. A client warning named `service connection failed` means the gateway rejected or couldn't open the target.

## Fix gateway startup failures

### The credential file is missing or empty

Create the first invite with the same `--users` path before starting `serve`:

```sh
tearenvd invite --users /var/lib/tearenv/users.json --identity alice
```

### Credential permissions are rejected

Protect the file and parent directory:

```sh
chmod 600 /var/lib/tearenv/users.json
chmod 700 /var/lib/tearenv
```

### The credential JSON can't be parsed or validated

Restore the last known-good backup or fix the named record during a maintenance window. Check JSON syntax with a parser that won't print the protected contents into shared logs:

```sh
jq empty /var/lib/tearenv/users.json
```

Common validation problems are malformed SHA256 hex, invalid identity or alias names, invalid `host:port` targets, and duration strings that don't use Go duration syntax.

### The SSH listen address is already in use

Check the configured port:

```sh
ss -ltnp | grep ':2222'
```

Stop the other listener or change `--listen`. Developers must use the same published address and known-hosts entry.

### Kubernetes scaler configuration fails outside a pod

`--scaler kubernetes` loads only in-cluster configuration. Running it on a workstation, even with a valid `KUBECONFIG`, reports an in-cluster configuration error. Run `tearenvd` in a Kubernetes pod, or omit the scaler for static services.

## Fix target and scaling failures

### The server logs `service target is unavailable`

For a static grant, test DNS and TCP from the `tearenvd` network namespace:

```sh
getent hosts api-alice.dev-alice.svc.cluster.local
nc -vz api-alice.dev-alice.svc.cluster.local 50051
```

Check the target port, service selector, network policy, and application listener. The target must be reachable from the gateway, not from the developer laptop.

### A Kubernetes workload doesn't scale up

Confirm the grant uses lowercase `deployment` or `statefulset` and the exact namespace and workload name. Check RBAC as the gateway service account:

```sh
kubectl auth can-i get deployments/scale \
  --as=system:serviceaccount:tearenv-system:tearenvd \
  -n dev-alice

kubectl auth can-i update deployments/scale \
  --as=system:serviceaccount:tearenv-system:tearenvd \
  -n dev-alice
```

Then inspect gateway logs and Kubernetes events:

```sh
kubectl -n tearenv-system logs deployment/tearenvd
kubectl -n dev-alice get events --sort-by=.lastTimestamp
```

### The workload scales up but the connection times out

tearenvd waits for the grant's target to accept TCP, not for a Kubernetes Ready condition. Check DNS, the Service selector and port, endpoints, pod listener, and network policy from the gateway pod. Increase `--ready-timeout` only after confirming the workload genuinely needs more startup time.

A readiness timeout triggers a best-effort scale-down to zero when no connection became active.

### The workload never scales down

Check for open TCP connections in application pools. The idle timer starts after the final proxied connection closes, and any new connection cancels it. Confirm that the grant has workload metadata and a finite `idle-timeout`.

If two aliases share the same workload tuple, activity through either alias keeps the workload running. This is intentional.

### Downscale fails

Look for `service downscale failed`, then verify `update` permission on the scale subresource and check Kubernetes API errors. A failed timer operation leaves the workload running and doesn't automatically schedule another retry; a later connection and idle cycle can trigger another attempt.

## Collect useful diagnostics

When reporting a problem, include sanitized versions of:

- The exact command and error.
- Client and gateway timestamps around the failure.
- The gateway address, alias, and identity, but no invite or token.
- The target type and workload tuple, without sensitive private DNS if the report leaves your organization.
- Relevant Kubernetes scale state, events, and RBAC checks.

Never include the client profile, plaintext invite, personal token, SSH private host key, or full credential file.
