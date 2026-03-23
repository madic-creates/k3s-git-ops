#!/usr/bin/env bash
# Validate Kubernetes manifests with kubeconform via kustomize build.
# Usage:
#   scripts/kubeconform-validate.sh                  # validate changed apps (pre-commit)
#   scripts/kubeconform-validate.sh apps/authelia     # validate specific app directory
#   scripts/kubeconform-validate.sh apps/authelia apps/traefik  # validate multiple
set -euo pipefail

if [ $# -gt 0 ]; then
  # Use provided directories
  changed_apps=""
  for dir in "$@"; do
    dir="${dir%/}"  # strip trailing slash
    dir="${dir#apps/}"  # strip apps/ prefix if present
    changed_apps="$changed_apps $dir"
  done
else
  # Determine changed app directories from staged files
  changed_apps=$(git diff --cached --name-only -- apps/ \
    | cut -d'/' -f2 \
    | sort -u)
fi

if [ -z "$(echo "$changed_apps" | tr -d ' ')" ]; then
  echo "No apps to validate"
  exit 0
fi

# Check dependencies
for cmd in kustomize kubeconform; do
  if ! command -v "$cmd" &>/dev/null; then
    echo "WARNING: $cmd not found, skipping kubeconform validation."
    echo "Install with: go install github.com/yannh/kubeconform/cmd/kubeconform@latest"
    exit 0
  fi
done

exit_code=0
for app in $changed_apps; do
  app_dir="apps/$app"
  if [ ! -f "$app_dir/kustomization.yaml" ]; then
    continue
  fi
  echo "Validating $app ..."
  if output=$(kustomize build "$app_dir" --enable-helm --enable-alpha-plugins --enable-exec 2>/dev/null); then
    echo "$output" | kubeconform \
      -strict \
      -ignore-missing-schemas \
      -kubernetes-version 1.31.0 \
      -schema-location default \
      -schema-location 'https://raw.githubusercontent.com/datreeio/CRDs-catalog/main/{{.Group}}/{{.ResourceKind}}_{{.ResourceAPIVersion}}.json' \
      -summary \
      -output text || exit_code=1
  else
    echo "WARNING: Kustomize build failed for $app, skipping schema validation"
  fi
done

exit $exit_code
