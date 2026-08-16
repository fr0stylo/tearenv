#!/bin/sh
set -eu

chart_output="$(mktemp)"
kustomize_output="$(mktemp)"
trap 'rm -f "$chart_output" "$kustomize_output"' EXIT

helm lint deploy/helm/tearenv
helm template tearenv deploy/helm/tearenv --namespace tearenv-system >"$chart_output"
helm template tearenv deploy/helm/tearenv \
  --namespace tearenv-system \
  --values deploy/helm/tearenv/examples/production-values.yaml \
  >/dev/null
kubectl kustomize deploy/kustomize/overlays/default >"$kustomize_output"

for rendered in "$chart_output" "$kustomize_output"; do
  grep -q -- '--registrations=/var/lib/tearenv/registrations' "$rendered"
  grep -q -- '--registration-token-file=' "$rendered"
  grep -q 'name: registration' "$rendered"
  grep -q 'path: /readyz' "$rendered"
  if grep -q -- '--authorized-keys' "$rendered"; then
    echo "obsolete --authorized-keys flag found in rendered deployment" >&2
    exit 1
  fi
  if grep -q 'image: ghcr.io/fr0stylo/tearenv:latest' "$rendered"; then
    echo "mutable latest image found in rendered deployment" >&2
    exit 1
  fi
done

helm template tearenv deploy/helm/tearenv \
  --namespace tearenv-system \
  --set registration.ingress.enabled=true \
  --set registration.ingress.host=tearenv.example.com \
  --set registration.ingress.tls.secretName=tearenv-registration-tls \
  --set blueprint.enabled=true \
  --set-file blueprint.document=deploy/kustomize/overlays/blueprint/environment-blueprint.yaml \
  >/dev/null

if helm template tearenv deploy/helm/tearenv \
  --namespace tearenv-system \
  --set registration.ingress.enabled=true \
  --set registration.ingress.host=tearenv.example.com \
  --set registration.ingress.tls.enabled=false \
  >/dev/null 2>&1; then
  echo "registration Ingress rendered without TLS" >&2
  exit 1
fi

kubectl kustomize deploy/kustomize/overlays/blueprint >/dev/null
kubectl kustomize deploy/kustomize/overlays/load-balancer >/dev/null
kubectl kustomize deploy/kustomize/overlays/cluster-wide >/dev/null
