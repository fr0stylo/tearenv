#!/bin/sh
set -eu

helm_repository_config="$(mktemp)"
helm_repository_cache="$(mktemp -d)"
trap 'rm -f "$helm_repository_config"; rm -rf "$helm_repository_cache"' EXIT

export HELM_REPOSITORY_CONFIG="$helm_repository_config"
export HELM_REPOSITORY_CACHE="$helm_repository_cache"

helm repo add dex https://charts.dexidp.io
helm dependency build deploy/helm/tearenv
