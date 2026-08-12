# tearenv documentation

`tearenv` gives developers local TCP ports for private development services without giving them private hostnames or general-purpose SSH forwarding. The `tearenvd` gateway authenticates each identity, decides which named services it can reach, and can start and stop Kubernetes workloads around active connections.

Start with the guide that matches what you're doing:

- [Understand why tearenv exists](overview.md) explains the problem, trust boundary, architecture, and connection lifecycle.
- [Try tearenv locally](getting-started.md) walks through a complete local setup from build to first proxied request.
- [Use tearenv as a developer](developer-guide.md) covers login, service discovery, local port selection, profiles, and day-to-day use.
- [Choose an authentication method](authentication.md) covers invite tokens, Kubernetes-managed public keys, security constraints, and the OIDC extension point.
- [Run tearenvd](operator-guide.md) covers identities, grants, static services, Kubernetes scaling, storage, and operations.
- [Define reusable team environments](environment-blueprints.md) covers the blueprint schema, namespace isolation, service declarations, and the planned selection boundary.
- [Deploy on Kubernetes](kubernetes-deployment.md) covers the container image, Helm chart, Kustomize overlays, persistence, and scaler RBAC.
- [Look up commands and file formats](reference.md) documents every CLI option, environment variable, default, naming rule, and data file.
- [Diagnose a problem](troubleshooting.md) maps common errors and symptoms to checks and fixes.
- [Review the security model](security.md) explains what tearenv permits, what it blocks, and how to deploy it safely.
- [Develop and test tearenv](development.md) covers the repository layout and test suites.

## Know the two programs

The repository builds two binaries:

| Program    | Audience   | Purpose                                                                           |
| ---------- | ---------- | --------------------------------------------------------------------------------- |
| `tearenv`  | Developers | Redeems an invite, lists authorized services, and opens local TCP listeners.      |
| `tearenvd` | Operators  | Runs the SSH gateway, manages invites and grants, and initializes team blueprints. |

`tearenv` is not an SSH shell, VPN, or network overlay. It exposes only the aliases granted to the authenticated identity. `tearenvd` resolves those aliases to server-side targets that never appear in the client catalog.

## Check the current limitations

The current CLI can create or replace invites, register Kubernetes public keys, create or replace service grants, and initialize blueprint YAML. It doesn't yet store a blueprint catalog, apply blueprint resources, or let developers request an environment. It also doesn't include commands to list identities, remove a public key, revoke an identity, remove a grant, or inspect the policy store. Treat `.data/users.json` as protected application state, back it up, and use a controlled maintenance procedure if you need to remove records manually.

Only the Kubernetes scaler is included. Static targets need no scaler. Docker, containerd, and other runtimes would require another implementation of the scaler interface.
