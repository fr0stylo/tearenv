# Install tearenv with Kustomize

The default overlay creates `tearenv-system`, a single gateway, persistent state, and scale permissions for workloads in that namespace. Create the registration Secret before applying it:

```sh
kubectl create namespace tearenv-system
openssl rand -base64 36 > tearenv-registration-token
chmod 600 tearenv-registration-token
kubectl create secret generic tearenvd-registration \
  --namespace tearenv-system \
  --from-file=token=tearenv-registration-token
```

Set your image and replace the placeholder policy in `overlays/default`, then apply it:

```sh
kubectl apply -k deploy/kustomize/overlays/default
```

Use `overlays/load-balancer` to publish only the SSH service, or `overlays/cluster-wide` when the gateway must scale existing workloads in every namespace. The registration and metrics Services remain cluster-local.

Use `overlays/blueprint` to mount the starter team blueprint and create one namespace per authenticated identity. Edit `overlays/blueprint/environment-blueprint.yaml` before applying the overlay. This overlay installs cluster-wide create and patch permissions for the supported blueprint resources.

See [the Kubernetes deployment guide](../../docs/kubernetes-deployment.md) for the complete setup and upgrade workflow.
