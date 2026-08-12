# Understand why tearenv exists

Remote development resources often sit on private networks and cost money while idle. Giving every developer network-wide access makes private addresses part of their local configuration, broadens the security boundary, and leaves no natural place to start a sleeping workload before a connection reaches it.

tearenv solves that problem with identity-aware, named TCP access. A developer connects to a familiar local address such as `127.0.0.1:5432`. The gateway authorizes the name `postgres` for that developer, optionally starts its workload, waits for its TCP endpoint, and proxies the bytes. When the final connection closes and the idle period expires, the gateway scales the workload back to zero.

## Keep private routing on the server

The client receives only a service name and a suggested local port. For example, it may learn that `postgres` should listen locally on port `5432`; it never receives `postgres.dev-alice.svc.cluster.local:5432` or the associated Kubernetes workload metadata.

The same alias can point to a different target for each identity. Granting `postgres` to Alice doesn't expose Bob's database to Alice, and it doesn't make Alice's database visible to Bob.

```text
database client
    |
    v
127.0.0.1:5432
    |
    v
tearenv -- authenticated SSH --> tearenvd
                                  | authorize identity + alias
                                  | optionally scale workload up
                                  | wait for target TCP readiness
                                  v
                         private service target
                                  |
                                  v
                         idle timer -> scale to zero
```

## Split developer convenience from operator control

`tearenv` owns the developer-side experience: login credentials, host verification, service discovery, and local listeners. Existing tools keep using localhost and don't need to understand SSH or Kubernetes.

`tearenvd` owns policy and private infrastructure details. Operators choose the identity, alias, target, suggested local port, workload, startup timeout, and idle timeout. This is why the two programs live in one repository: their small SSH protocol is one product boundary, and changes to either side can be tested end to end.

## Reuse team definitions without trusting user-supplied identity

An `EnvironmentBlueprint` moves the repeated Kubernetes shape into a team-owned definition. The blueprint contains resource templates, exposed services, and scale policies. It doesn't contain the identity that will run it.

When `tearenvd` starts with `--blueprint`, a successful SSH authentication triggers Kubernetes reconciliation before the login is accepted. `tearenvd` takes the identity from the verified SSH session and combines it with the configured blueprint name. The client can't override that identity or namespace.

Each environment namespace includes both values, such as `tearenv-alice-web-development`. Bob receives a separate namespace when he logs in to the same gateway. Blueprint services are added to that identity's catalog and use the existing connection-driven scale engine.

The current daemon has one configured blueprint and applies it automatically. A future catalog and request protocol can let a developer choose among multiple team-approved blueprints without changing the identity boundary.

## Follow a connection through the gateway

1. An operator creates a one-time invite and grants one or more aliases to its identity.
2. The developer verifies the gateway's SSH host key and redeems the invite.
3. `tearenv` saves a personal token in an owner-only local profile.
4. `tearenv services` authenticates. If a blueprint is configured, `tearenvd` reconciles the identity namespace and resources before returning the catalog.
5. `tearenv connect` authenticates, binds local listeners, and waits.
6. A local application opens a TCP connection. `tearenv` requests the alias with port zero over an SSH `direct-tcpip` channel.
7. `tearenvd` resolves the alias against current server policy. The client can't substitute a hostname or port.
8. For a managed workload, `tearenvd` scales it up once and retries the target every 500 milliseconds until it accepts TCP or the readiness timeout expires.
9. The gateway proxies bytes in both directions and counts the connection as active.
10. When the last connection closes, the idle timer begins. A new connection cancels the timer. Expiration scales the workload to zero.

Aliases that point to the same workload kind, namespace, and name share connection activity and one lifecycle. This prevents one alias from scaling down a workload while another alias is still using it.

## Use it for TCP services

tearenv is protocol-agnostic after the connection opens. PostgreSQL, Redis, ClickHouse native connections, gRPC, HTTP, and other TCP protocols can pass through it. It doesn't proxy UDP, provide an HTTP ingress, terminate database authentication, or discover services automatically.

Application-level connection pools matter. A pool that keeps an idle TCP connection open is still active from tearenv's perspective, so the workload won't scale down until that connection closes.

## Know what tearenv doesn't replace

tearenv doesn't replace service authentication or encryption. Traffic is protected inside the SSH connection between `tearenv` and `tearenvd`, but the gateway-to-target connection is ordinary TCP. Keep database credentials, TLS, workload network policies, and application authorization appropriate for the environment.

It also doesn't provide high-availability coordination between multiple gateway replicas. Lifecycle activity and timers are kept in each process. Run one active `tearenvd` instance per managed workload set unless you add external coordination.
