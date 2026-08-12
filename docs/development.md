# Develop and test tearenv

The repository is a Go module with no generated application code. Keep client, server, protocol, and lifecycle changes covered at their narrowest layer, then run the end-to-end path when behavior crosses the binary boundary.

## Find the relevant package

```text
cmd/tearenv/         developer CLI
cmd/tearenvd/        gateway and administrative CLI
internal/client/     enrollment, catalog, SSH connection, and local listeners
internal/authorization/ authentication providers, credentials, invites, and service policy
internal/blueprint/  team blueprint schema, starter document, strict loading, and validation
internal/profile/    protected client profile persistence
internal/protocol/   shared SSH request names and public catalog types
internal/server/     SSH transport, host keys, proxy channels, and lifecycle
internal/scaler/     backend-neutral scale interface
internal/kube/       in-cluster scaler and authorized-key Secret updates
internal/proxy/      bidirectional stream copying
e2e/                 compiled-binary and Kind scenarios
deploy/helm/         installable Helm chart
deploy/kustomize/    Kustomize base and overlays
deploy/kubernetes/   minimal scaler RBAC example
```

Authentication providers implement `authorization.Authenticator` and can be composed with `authorization.NewChain`. A provider verifies one credential method and returns the verified identity. The server rejects a provider result that doesn't match the requested SSH identity. Service grants remain independent through `authorization.Policy`.

Runtime backends implement `scaler.Backend`. A new backend receives a workload kind, namespace-like scope, name, and replica count. The lifecycle gateway owns connection tracking, readiness polling, and idle timers independently of the backend.

Environment blueprint changes belong in `internal/blueprint`. Keep the versioned schema independent of authenticated environment requests: team operators own blueprint definitions, while the future request path supplies only a blueprint reference and gets identity from the authenticated server context.

## Run the standard checks

Run:

```sh
make check
```

This runs `go vet ./...`, the complete suite with the race detector, and both binary builds.

Use narrower targets during development:

```sh
make test
make test-race
make vet
make build
make e2e
make manifests
```

`make e2e` compiles both programs and verifies invite redemption, identity authentication, catalog authorization, local forwarding, invite single use, and cross-identity rejection.

`make manifests` lints and renders the Helm chart and every Kustomize overlay. Docker image construction is also checked in CI.

## Run the Kubernetes lifecycle test

Install Docker, Kind, kubectl, and Go, and make sure the Docker daemon is running. Then run:

```sh
make e2e-kind
```

The opt-in test creates a disposable Kind cluster and local image. It checks identity-specific routing for two developers, zero-replica startup, concurrent request bursts, repeat cold starts, idle downscale, and pod removal. It isn't included in `make check` or ordinary `go test ./...` because it creates containers and a cluster.

Retain a failed cluster for inspection:

```sh
TEARENV_KEEP_KIND_CLUSTER=1 make e2e-kind
```

The test prints the retained kubectl context. Without that variable, cleanup deletes the cluster and local image even after failure.

## Add behavior with focused tests

Authentication, credential, and policy changes belong in `internal/authorization`. Blueprint schema and reference validation belong in `internal/blueprint/blueprint_test.go`. Lifecycle timing and shared-workload activity belong in `internal/server/lifecycle_test.go`. Kubernetes API behavior belongs in `internal/kube`, which uses injected clients. Client/server integration belongs in `internal/client/integration_test.go`; binary behavior belongs in `e2e/e2e_test.go`.

Use short idle and readiness durations only in tests. Production defaults and examples should remain realistic and shouldn't make correctness depend on tight scheduler timing.

## Preserve the security boundary

Protocol changes deserve extra review. Don't add general shell handling or accept arbitrary hostnames and ports in direct TCP channels. Keep the catalog limited to information developers need, and resolve authorization from live server policy when each channel opens.

Maintain owner-only modes and atomic replacement for files containing credentials. Avoid logging plaintext invites, tokens, profiles, or private keys.

## Format Go and documentation

Format Go code with:

```sh
make fmt
```

The documentation style uses conversational, task-first prose, sentence-case headings, American spelling, and verified command examples. If Bun and Prettier are available in your environment, format Markdown with:

```sh
bun run prettier --write 'docs/**/*.md'
```

Review the resulting diff because code blocks and tables are part of the command reference.
