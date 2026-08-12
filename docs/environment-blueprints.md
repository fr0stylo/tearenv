# Define reusable team environments with blueprints

An `EnvironmentBlueprint` is a team-owned definition of one environment shape. It describes Kubernetes resources, exposed services, and scale policy without belonging to a specific person. The planned request flow lets people select a blueprint by its `metadata.name` when they need an environment.

The same blueprint will be selectable by many authenticated identities. One identity will also be able to select different blueprints, such as `web-development` and `data-development`.

Blueprints are an alpha configuration API. The current CLI initializes the document but doesn't apply it to a cluster yet.

## Keep definition, selection, and identity separate

| Concern | Source of truth |
| --- | --- |
| Available environment shapes | Team-owned blueprint catalog |
| Requested environment shape | Blueprint name selected by the authenticated person |
| Environment owner | Identity from the authenticated SSH session |
| Kubernetes namespace | Rendered identity slug plus blueprint name |

This separation lets a team update one reusable definition without copying it into every identity record. It also prevents a request from claiming another identity.

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

`metadata.name` is the future catalog and selection key. It must be a lowercase Kubernetes DNS label, such as `web-development`.

## Separate each identity and blueprint selection

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

`IdentitySlug` represents the authenticated identity converted to a Kubernetes DNS label. `BlueprintName` comes from the team-owned blueprint's `metadata.name`. Requiring both placeholders prevents identities from sharing a namespace and prevents two selected blueprints from colliding for the same identity.

The provisioning workflow will render and, when necessary, shorten the values to fit the Kubernetes namespace limit. It will then inject the namespace into each resource, so resource templates must omit `metadata.namespace`.

## Let people select, not define, blueprints

The team controls which blueprint documents are available. A future environment request will contain a blueprint reference, while the server will take the identity from the authenticated SSH session. The request won't accept an identity field, so a person can't ask tearenv to create another identity's environment.

The selected blueprint name and authenticated identity together identify the environment instance. Authorization can later restrict which team blueprints each identity may select without changing the blueprint document.

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

## Know what comes next

`tearenvd blueprint init` only writes configuration. It doesn't contact Kubernetes or update identity grants.

The apply workflow will need to look up the selected blueprint from the team's catalog, combine it with the authenticated identity, create or reconcile the namespace and resources, derive each internal Service address, and write identity-bound grants that reuse the existing connection-driven scaler. Keeping that work behind the versioned blueprint contract lets provisioning evolve without changing the developer tunnel protocol.
