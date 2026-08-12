# Define reusable team environments with blueprints

An `EnvironmentBlueprint` is a team-owned definition of one environment shape. It describes Kubernetes resources, exposed services, and scale policy without belonging to a specific person. Configure `tearenvd` with that blueprint to create an isolated environment for every authenticated identity.

The same blueprint works for many identities. Invite enrollment and every later login reconcile that identity's namespace and resources before the SSH session is accepted. Reconciliation uses Kubernetes server-side apply, so repeat logins update the same environment instead of creating duplicates.

Blueprints are an alpha configuration API. The current gateway accepts one blueprint file. Developer selection among several team templates is still planned.

## Keep definition, selection, and identity separate

| Concern | Source of truth |
| --- | --- |
| Available environment shapes | Team-owned blueprint files |
| Active environment shape | Blueprint file mounted by the operator |
| Environment owner | Identity from the authenticated SSH session |
| Kubernetes namespace | Rendered identity slug plus blueprint name |

This separation lets a team update one reusable definition without copying it into every identity record. The gateway gets the owner from verified SSH authentication, so a client can't claim another identity.

## Initialize a starter blueprint

Run:

```sh
tearenvd blueprint init > environment-blueprint.yaml
```

Set the blueprint name when you maintain more than one environment shape:

```sh
tearenvd blueprint init \
  --name web-development \
  > web-development.yaml
```

The generated blueprint contains an nginx Deployment at zero replicas, a Service, and one tearenv service named `web`. Replace those resource templates with the services that make up your development environment.

`metadata.name` identifies the configured environment shape and forms part of every rendered namespace. It must be a lowercase Kubernetes DNS label, such as `web-development`.

## Enable provisioning on login

Mount the reviewed blueprint into the `tearenvd` pod, then run:

```sh
tearenvd serve \
  --users /var/lib/tearenv/users.json \
  --host-key /var/lib/tearenv/ssh_host_ed25519_key \
  --scaler kubernetes \
  --blueprint /etc/tearenv/environment-blueprint.yaml
```

`--blueprint` requires `--scaler kubernetes` and in-cluster Kubernetes credentials. During invite enrollment, token authentication, or public-key authentication, `tearenvd` creates or patches the namespace and every namespaced object in `spec.resources`. If discovery, validation, or an API operation fails, authentication fails and the gateway doesn't publish the blueprint services for that session. An invite isn't consumed when provisioning prevents enrollment.

Use the Helm chart's `blueprint` values or the Kustomize `overlays/blueprint` overlay to mount the file and install the required ClusterRole. `EnvironmentBlueprint` is tearenv configuration, not a Kubernetes CRD, so don't pass the file to `kubectl apply`.

## Separate each identity and blueprint name

Every namespace name template must contain both `{{ .IdentitySlug }}` and `{{ .BlueprintName }}` exactly once:

```yaml
spec:
  namespace:
    nameTemplate: tearenv-{{ .IdentitySlug }}-{{ .BlueprintName }}
    labels:
      app.kubernetes.io/managed-by: tearenv
      tearenv.io/identity: "{{ .IdentitySlug }}"
      tearenv.io/blueprint: "{{ .BlueprintName }}"
```

`IdentitySlug` represents the authenticated identity converted to a Kubernetes DNS label. `BlueprintName` comes from the team-owned blueprint's `metadata.name`. Requiring both placeholders prevents identities from sharing a namespace and leaves room for multiple blueprint selections later.

The provisioning workflow renders and, when necessary, shortens values to fit the Kubernetes namespace limit. Identities that need case folding or character replacement receive a short hash suffix to prevent normalized names from colliding. The renderer injects the namespace into a copy of every resource, so resource templates must omit `metadata.namespace`.

## Let operators define the active blueprint

The team controls the blueprint mounted into the gateway. Developers don't upload resource definitions, choose a namespace, or send an identity field. This keeps images, commands, volumes, service accounts, and network policy under operator review.

The current gateway provisions the configured blueprint automatically for every authenticated identity. A future environment request can add blueprint selection and per-template authorization without allowing a client-controlled identity.

## Declare resources before exposing services

Put namespaced Kubernetes objects under `spec.resources`. Each object needs `apiVersion`, `kind`, and `metadata.name`. Resource identities must be unique within the blueprint.

An exposed service references both a core Kubernetes Service and a scalable workload from that resource list:

```yaml
spec:
  services:
    - name: web
      localPort: 8080
      target:
        service: workspace
        port: 80
      scale:
        targetRef:
          apiVersion: apps/v1
          kind: Deployment
          name: workspace
        replicas: 1
        readyTimeout: 2m
        idleTimeout: 10m
```

`target.service` becomes the in-namespace network destination. `scale.targetRef` identifies the workload that tearenv starts on the first connection and returns to zero after the last connection has been idle. The first API version accepts `apps/v1` Deployments and StatefulSets, matching the current Kubernetes scaler.

Set the resource's initial replicas to `0`. The scale policy's `replicas` value must be at least `1`. `readyTimeout` must be positive, while `idleTimeout` can be `0` for immediate scale-down.

After reconciliation succeeds, each declared service appears in that identity's catalog. Its internal target uses `<service>.<namespace>.svc.cluster.local:<port>`, and its workload metadata points to the same namespace. If a direct grant has the same alias, the blueprint service takes precedence.

Kubernetes discovery must identify every object as namespaced. Cluster-scoped objects such as `ClusterRole` are rejected even if the service account could create them. The supplied deployment RBAC covers common core, `apps`, `batch`, and `networking.k8s.io` resources. Extend the ClusterRole when a reviewed blueprint uses another namespaced API.

## Follow the validation contract

The blueprint loader rejects unknown top-level fields and validates references before provisioning code can consume the document.

| Field or relationship | Requirement |
| --- | --- |
| `apiVersion` | Exactly `tearenv.io/v1alpha1` |
| `kind` | Exactly `EnvironmentBlueprint` |
| `metadata.name` | Kubernetes DNS label and team catalog key |
| `namespace.nameTemplate` | Contains `IdentitySlug` and `BlueprintName` exactly once |
| `resources` | Unique `apiVersion`, `kind`, and `metadata.name`; no explicit namespace |
| `services[].target` | References a declared core Kubernetes Service and exposed port |
| `services[].scale.targetRef` | References an `apps/v1` Deployment or StatefulSet declared in the blueprint |
| Scalable resource replicas | Starts at `0` |
| Active replicas | At least `1` |
| Lifecycle timeouts | Positive readiness timeout and non-negative idle timeout |

## Understand reconciliation limits

Server-side apply creates missing objects and reconciles fields owned by `tearenv-blueprint`. Removing an object from the blueprint doesn't delete the previously applied object. Delete retired environment resources through a controlled operator workflow.

Blueprint-backed service policy is held in gateway memory after a successful login. A restart is safe: the next authentication reconciles the existing environment and republishes its services. Keep one active gateway replica because service lifecycle counters and scale-down timers still aren't coordinated across replicas.
