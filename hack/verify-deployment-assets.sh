#!/bin/sh
set -eu

chart_output="$(mktemp)"
kustomize_output="$(mktemp)"
oidc_chart_output="$(mktemp)"
oidc_kustomize_output="$(mktemp)"
dex_chart_output="$(mktemp)"
trap 'rm -f "$chart_output" "$kustomize_output" "$oidc_chart_output" "$oidc_kustomize_output" "$dex_chart_output"' EXIT

sh hack/prepare-helm-dependencies.sh
helm lint deploy/helm/tearenv
helm template tearenv deploy/helm/tearenv --namespace tearenv-system >"$chart_output"
helm template tearenv deploy/helm/tearenv \
  --namespace tearenv-system \
  --values deploy/helm/tearenv/examples/production-values.yaml \
  >/dev/null
kubectl kustomize deploy/kustomize/overlays/default >"$kustomize_output"

helm template tearenv deploy/helm/tearenv \
  --namespace tearenv-system \
  --set registration.authMode=oidc \
  --set registration.oidc.issuerURL=https://id.example.com \
  --set registration.oidc.clientID=tearenv-cli \
  --set registration.oidc.audience=tearenv \
  --set registration.oidc.sshUserCA.existingSecret=tearenv-ssh-user-ca \
  >"$oidc_chart_output"
helm template tearenv deploy/helm/tearenv \
  --namespace tearenv-system \
  --values deploy/helm/tearenv/examples/dex-github-values.yaml \
  >"$dex_chart_output"
helm template tearenv deploy/helm/tearenv \
  --namespace tearenv-system \
  --values deploy/helm/tearenv/examples/external-keycloak-values.yaml \
  >/dev/null
kubectl kustomize deploy/kustomize/overlays/oidc >"$oidc_kustomize_output"

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

grep -q 'app.kubernetes.io/name: dex' "$dex_chart_output"
grep -q -- '--oidc-issuer-url=https://auth.example.com' "$dex_chart_output"
grep -q -- '--oidc-subject-token-type=id-token' "$dex_chart_output"

for rendered in "$oidc_chart_output" "$oidc_kustomize_output"; do
  grep -q -- '--registration-auth-mode=oidc' "$rendered"
  grep -q -- '--oidc-issuer-url=https://id.example.com' "$rendered"
  grep -q -- '--oidc-subject-token-type=id-token' "$rendered"
  grep -q -- '--ssh-user-ca-key=' "$rendered"
  if grep -q -- '--registration-token-file=' "$rendered"; then
    echo "registration token flag found in OIDC deployment" >&2
    exit 1
  fi
done

if helm template tearenv deploy/helm/tearenv \
  --namespace tearenv-system \
  --set registration.authMode=oidc \
  >/dev/null 2>&1; then
  echo "incomplete OIDC configuration rendered successfully" >&2
  exit 1
fi

if helm template tearenv deploy/helm/tearenv \
  --namespace tearenv-system \
  --values deploy/helm/tearenv/examples/dex-github-values.yaml \
  --set registration.oidc.clientID=unregistered-client \
  >/dev/null 2>&1; then
  echo "bundled Dex rendered with a mismatched public client" >&2
  exit 1
fi

if helm template tearenv deploy/helm/tearenv \
  --namespace tearenv-system \
  --set registration.authMode=oidc \
  --set registration.oidc.provider=bundledDex \
  --set registration.oidc.sshUserCA.existingSecret=tearenv-ssh-user-ca \
  >/dev/null 2>&1; then
  echo "bundled Dex provider rendered without dex.enabled=true" >&2
  exit 1
fi

if helm template tearenv deploy/helm/tearenv \
  --namespace tearenv-system \
  --set dex.enabled=true \
  --set dex.config.issuer=https://auth.example.com \
  >/dev/null 2>&1; then
  echo "Dex rendered while token authentication was active" >&2
  exit 1
fi

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
