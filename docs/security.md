# Review the security model

tearenv narrows remote development access to an authenticated identity and an operator-defined service alias. It doesn't make an untrusted endpoint safe by itself; deployment choices still determine the strength of the boundary.

## Trust these components deliberately

The developer trusts the local machine, the `tearenv` binary, and the verified `tearenvd` host key. The operator trusts the gateway host, credential storage, workload scaler permissions, private DNS, and target services.

The gateway is a privileged policy enforcement point. It can reach every configured target and, with the Kubernetes backend, change replica counts for workloads permitted by its service account. Harden it and restrict administrative access accordingly.

## Limit what an authenticated client can request

The SSH server enables only the application protocol needed by tearenv:

- Password authentication carries the invite or personal token inside SSH.
- Public-key authentication proves possession of an identity-bound private key.
- Enrollment requests can exchange a valid one-time invite for a personal token.
- Catalog requests return aliases and suggested local ports.
- Direct TCP channels accept a granted alias with port zero.

Shell sessions, arbitrary destination ports, reverse forwarding, and enrollment-time service access aren't enabled. Every service channel resolves current policy for the authenticated identity. A client can't supply the private hostname stored in the grant.

This boundary limits gateway-mediated access. It doesn't compensate for other network paths to the same private services.

## Verify the SSH host key

Keep the Ed25519 host private key stable and protected. Developers must compare its public fingerprint through a trusted channel before login. `ssh-keyscan` discovers a key but doesn't prove its identity.

Use `--insecure-skip-host-key-check` only for disposable local development. It makes the client accept any host key and enables machine-in-the-middle attacks that can capture an invite or personal token.

Plan host-key rotation like any SSH infrastructure change: publish the new fingerprint securely, update known-hosts entries, and investigate any unexpected mismatch.

## Protect bearer credentials

One-time invites and personal tokens are bearer secrets tied to an identity. Send invites through a protected channel, redeem them promptly, and don't place them in tickets, chat rooms, shell history, or CI logs.

The server persists SHA256 hashes of invites and tokens. The client must retain its plaintext token to authenticate, so its profile is more sensitive than the hash-only server record. Both client and server reject their JSON file when group or world permission bits are present.

Issuing a replacement invite doesn't immediately revoke an existing token. Redemption rotates the token. For immediate incident response, stop the gateway, remove the compromised identity and its access from a protected backup-derived credential document, preserve mode `0600`, and restart. The current CLI doesn't provide a revoke command.

## Keep SSH private keys off the cluster

Kubernetes login stores only public keys in the cluster and keeps the generated private key in an owner-only local file. Back up or rotate that local key according to your endpoint policy. Don't copy it into a Kubernetes Secret, container image, ticket, or shared profile.

Write access to the authorized-keys Secret is equivalent to tearenv identity administration. A writer can insert a key for any identity in the document. Restrict updates to an operator workflow or enforce the Kubernetes-to-tearenv identity mapping with admission policy. Kubernetes cluster access by itself doesn't prove which tearenv identity a caller should receive.

## Keep local listeners private

The default `127.0.0.1` listener restricts access to the developer machine. tearenv doesn't authenticate programs connecting to that local port. Binding `0.0.0.0`, a LAN address, or a shared container interface can let other processes or hosts use the developer's authenticated tunnel.

Local malware or another process running as the developer can also connect to loopback. Use normal endpoint hardening and application-level credentials.

## Restrict gateway-to-target traffic

SSH protects traffic only between the client and gateway. The gateway-to-target leg is TCP and may be plaintext unless the application protocol adds TLS. Continue to use database authentication, TLS, network policy, and target-side authorization.

Use firewall or Kubernetes NetworkPolicy rules so `tearenvd` can reach only intended service destinations. Identity-bound aliases prevent client-selected routing, while network controls limit the impact of a bad grant or compromised gateway.

Validate target ownership before granting it. DNS changes after a grant can redirect the gateway because targets are resolved when connections open.

## Keep scaler permissions narrow

The supplied manifest grants cluster-wide `get` and `update` on Deployment and StatefulSet scale subresources. Replace the ClusterRole and ClusterRoleBinding with namespace-scoped Roles and RoleBindings when possible.

The scaler doesn't create, patch, or delete workloads. It changes desired replicas only. Kubernetes admission controls and audit logs can add another enforcement and investigation layer.

Don't run multiple uncoordinated gateway replicas against the same managed workloads. Each replica has independent active counts and timers, so one could scale down a workload used through another.

## Keep environment blueprints under team control

An environment blueprint contains Kubernetes resource templates. Treat permission to create or modify the team blueprint catalog as infrastructure-administration access, not ordinary developer access. Review images, commands, volumes, service accounts, security contexts, and network exposure before making a blueprint selectable.

The planned environment request accepts a blueprint reference, not an identity. `tearenvd` must bind the request to the authenticated SSH identity and derive the namespace from that identity plus the selected blueprint name. Don't add a client-controlled identity field to this path.

Namespace separation is one layer, not the whole boundary. Apply ResourceQuota, LimitRange, RBAC, Pod Security admission, and NetworkPolicy appropriate for untrusted workloads. The current implementation initializes blueprint YAML but doesn't apply or authorize it yet.

## Treat policy and logs as sensitive

The server credential file contains identity mappings, private targets, aliases, and workload metadata even though credentials are hashed. Keep it out of source control, container images, client machines, and unprotected backups.

Gateway logs include identities, remote addresses, service aliases, and server-side targets. Restrict log access and retention. The implementation doesn't log plaintext tokens or invites; avoid adding command wrappers that do.

## Understand cryptographic scope

Hashing protects stored credentials from direct disclosure, but unsalted SHA256 isn't a password-hardening scheme. tearenv-generated invites and tokens contain 32 random bytes, making offline guessing impractical. Don't replace them with human-chosen tokens through legacy file fields.
