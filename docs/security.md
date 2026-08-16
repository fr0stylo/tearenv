# Review the security model

tearenv narrows remote development access to an authenticated identity and an operator-defined service alias. It doesn't make an untrusted endpoint safe by itself; deployment choices still determine the strength of the boundary.

## Trust these components deliberately

The developer trusts the local machine, the `tearenv` binary, the registration API, and the verified `tearenvd` host key. The operator trusts the gateway host, registration and policy storage, workload scaler permissions, private DNS, and target services.

The gateway is a privileged policy enforcement point. It can reach every configured target and, with the Kubernetes backend, change replica counts for workloads permitted by its service account. Harden it and restrict administrative access accordingly.

## Limit what an authenticated client can request

The SSH server enables only the application protocol needed by tearenv:

- Public-key authentication proves possession of an identity-bound private key.
- Catalog requests return aliases and suggested local ports.
- Direct TCP channels accept a granted alias with port zero.

Shell sessions, arbitrary destination ports, reverse forwarding, and enrollment-time service access aren't enabled. Every service channel resolves current policy for the authenticated identity. A client can't supply the private hostname stored in the grant.

This boundary limits gateway-mediated access. It doesn't compensate for other network paths to the same private services.

## Verify the SSH host key

Keep the Ed25519 host private key stable and protected. Developers must compare its public fingerprint through a trusted channel before login. `ssh-keyscan` discovers a key but doesn't prove its identity.

Use `--insecure-skip-host-key-check` only for disposable local development. It makes the client accept any host key and enables machine-in-the-middle attacks against the tunnel.

Plan host-key rotation like any SSH infrastructure change: publish the new fingerprint securely, update known-hosts entries, and investigate any unexpected mismatch.

## Protect registration state and private keys

The current API accepts valid registrations by default. The first writer at a resource path establishes its immutable identity and public-key spec. This is simple and stateful, but it is not identity verification: an untrusted caller could register a key for an identity before its intended owner.

The default API listener is loopback-only. The Kubernetes packages configure bearer-token authentication with `--registration-token-file`. When remote registration is required, expose it only through a trusted TLS reverse proxy or Ingress and protect the token through your secret-management system.

The shared enrollment token proves permission to register, but it doesn't bind a caller to a particular identity. A token holder could claim any unclaimed identity, including one with existing grants. Use a private onboarding channel, rotate a disclosed token, and don't treat this baseline as untrusted multi-tenant identity federation.

The private key stays in the developer's owner-only local file. Anyone who obtains it can authenticate as that identity. The server-side registration documents contain public keys, but write access is security-sensitive because it controls authentication. Keep the store on protected persistent storage and out of source control.

## Keep SSH private keys off the cluster

`tearenv login` submits only the public key and keeps the generated private key in an owner-only local file. Back up or rotate that local key according to your endpoint policy. Don't copy it into a Kubernetes Secret, container image, ticket, or shared profile.

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

An environment blueprint contains Kubernetes resource templates. Treat permission to create or modify the mounted blueprint as infrastructure-administration access, not ordinary developer access. Review images, commands, volumes, service accounts, security contexts, and network exposure before enabling it.

`tearenvd` provisions only after an authentication provider verifies the SSH identity. The server passes that verified identity directly to the environment manager and derives the namespace from it plus the operator-controlled blueprint name. The client can't supply an environment identity or namespace.

Blueprint provisioning needs cluster-wide permission to create namespaces and to create or patch the allowed namespaced resources. This is broader than scaler-only access. Restrict the ClusterRole to APIs used by reviewed blueprints, protect the gateway service account, use admission policy to constrain resulting workloads, and monitor Kubernetes audit logs.

Namespace separation is one layer, not the whole boundary. Apply ResourceQuota, LimitRange, RBAC, Pod Security admission, and NetworkPolicy appropriate for developer workloads. Cluster-scoped blueprint objects are rejected, but a namespaced Secret, service account, or workload can still be sensitive.

Server-side apply doesn't prune objects removed from a blueprint. Use a controlled deletion workflow so an old object can't remain unnoticed after a template change.

## Treat policy and logs as sensitive

The server policy file contains identity mappings, private targets, aliases, and workload metadata. Keep it out of source control, container images, client machines, and unprotected backups.

Gateway logs include identities, remote addresses, service aliases, and server-side targets. Restrict log access and retention. The implementation doesn't log private keys; avoid adding command wrappers that do.
