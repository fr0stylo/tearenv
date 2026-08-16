# Review the developer flow

The invite-based flow is usable for a controlled team. Kubernetes self-registration isn't safe as a general self-service flow yet, and long-running connections don't recover when the gateway restarts.

## Fix Kubernetes identity impersonation first

Priority: high.

`tearenv login --method kubernetes` accepts a developer-supplied `--identity` and writes the generated public key under that identity in the shared Secret. Anyone who can update the Secret can claim another tearenv identity and receive its grants.

The behavior starts in [`login`](../cmd/tearenv/main.go), then passes the requested identity to [`UpsertAuthorizedKeySecret`](../internal/kube/authorized_keys.go). The authentication guide warns that Secret write access is identity-administration access, but direct Secret updates still aren't suitable as the default developer flow.

Use operator-controlled key registration, enforce a Kubernetes-to-tearenv identity mapping with admission policy, or add OIDC before allowing general self-registration.

## Recover when the gateway connection ends

Priority: high.

`tearenv connect` keeps local listeners open until its context is canceled or a listener fails. It doesn't monitor the SSH connection and doesn't reconnect.

After a gateway rollout or network interruption, the local ports remain bound even though new application connections fail. The previous `service ready` message is then misleading.

Update [`RunServices`](../internal/client/services.go) to watch the SSH connection. Either reconnect with bounded backoff or close every listener and exit with a nonzero status so the developer knows to restart the command.

## Protect an invite when profile storage fails

Priority: medium.

Token login redeems the one-time invite before saving the returned token to the local profile. If profile persistence fails because the directory is unwritable, the disk is full, or the final rename fails, the invite is consumed and the token is lost.

Preflight the profile destination before enrollment. A complete fix also needs a protected recovery path for failures that happen after the gateway returns the token.

## Verify the host key before trusting it

Priority: medium.

The developer guide currently appends `ssh-keyscan` output directly to `known_hosts`, then asks the developer to compare fingerprints. A mismatched key remains trusted until someone removes it manually, and checking a shared `known_hosts` file can print fingerprints for unrelated hosts.

Change the instructions to:

1. Scan the gateway key into a temporary file.
2. Compare that file's fingerprint with the operator-provided fingerprint.
3. Append the verified key to `known_hosts` only after it matches.

The operator handoff should include the identity, gateway address, host-key fingerprint, and one authentication method.

## Choose authentication before showing login commands

Priority: medium.

The developer guide starts as though everyone receives an invite, then introduces Kubernetes authentication later. The two flows have different prerequisites:

- Token login needs a one-time invite.
- Kubernetes login needs a kubeconfig context, namespace, Secret name, an existing grant, and permission to read and create or update that Secret.

Branch the onboarding instructions before login. The Kubernetes troubleshooting checks should include `create` permission because the command creates the Secret when it doesn't exist.

## Add credential lifecycle commands

Priority: lower.

The client has no `status` or `logout` command, and the operator CLI has no supported token revocation or public-key removal command. Deleting a local profile removes the credential from one machine but doesn't revoke its server-side access.

Add an explicit local logout command and an operator-controlled revocation flow before treating onboarding and offboarding as complete.

## Use this developer journey

The operator should provide a client binary or installation command, the gateway address, the verified host-key fingerprint, the assigned identity, and one authentication method. For Kubernetes authentication, the operator must also provide the context, namespace, Secret name, and narrowly scoped permissions.

After installation and host-key verification, the normal developer workflow should be:

```sh
tearenv login
tearenv services
tearenv connect postgres
```

Start with explicit service selection instead of connecting every grant. This avoids common local-port collisions and makes the active access clear.

Future lifecycle commands should make connection and credential state visible:

```sh
tearenv status
tearenv logout
```

## Keep the current strengths

The existing flow already protects profiles and private keys with owner-only permissions, verifies the gateway host key, exposes only identity-authorized aliases, hides private targets, and binds suggested listeners to loopback by default.

Focused tests for the CLI, client, profile, and Kubernetes key updater pass. The review didn't change application behavior.
