# Install tearenv with Kustomize

The default overlay creates `tearenv-system`, a single gateway, persistent state, and scale permissions for workloads in that namespace.

Set your image and replace the placeholder policy in `overlays/default`, then apply it:

```sh
kubectl apply -k deploy/kustomize/overlays/default
```

Use `overlays/load-balancer` to publish the SSH service, `overlays/public-keys` after creating the `tearenv-authorized-keys` Secret, or `overlays/cluster-wide` when the gateway must scale workloads in every namespace.

See [the Kubernetes deployment guide](../../docs/kubernetes-deployment.md) for the complete setup and upgrade workflow.
