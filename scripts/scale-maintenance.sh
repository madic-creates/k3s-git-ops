#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR=$( dirname -- "$( readlink -f -- "$0"; )"; )

ACTION="${1:-}"
shift || true

DRY_RUN=false
while (($#)); do
  case "$1" in
    --dry-run)
      DRY_RUN=true
      ;;
    --plan-file)
      PLAN_FILE="${2:-}"
      if [[ -z "${PLAN_FILE:-}" ]]; then
        echo "ERROR: --plan-file requires a value" >&2
        exit 2
      fi
      shift
      ;;
    *)
      echo "ERROR: Unknown argument: $1" >&2
      exit 2
      ;;
  esac
  shift || break
done

PLAN_FILE="${PLAN_FILE:-$SCRIPT_DIR/scale-maintenance-plan.yaml}"
ANNOTATION_KEY="maintenance.k3s-git-ops/original-replicas"

if [[ -z "$ACTION" ]]; then
  cat <<'USAGE'
Usage: scripts/scale-maintenance.sh <down|up|status> [--dry-run] [--plan-file <path>]

  down     Store current replicas (if needed) and scale workloads to zero.
  up       Restore replicas from annotations and clear maintenance markers.
  status   Show current replica counts and stored maintenance annotations.

Environment:
  PLAN_FILE: Override default scripts/scale-maintenance-plan.yaml
USAGE
  exit 1
fi

for tool in kubectl yq; do
  if ! command -v "$tool" >/dev/null 2>&1; then
    echo "ERROR: Required tool '$tool' not found in PATH" >&2
    exit 1
  fi
done

if [[ ! -f "$PLAN_FILE" ]]; then
  echo "ERROR: Plan file '$PLAN_FILE' does not exist" >&2
  exit 1
fi

if yq eval -r '.workloads | length' "$PLAN_FILE" >/dev/null 2>&1; then
  YQ_READ_CMD=(yq eval -r)
else
  YQ_READ_CMD=(yq -r)
fi

read_plan() {
  "${YQ_READ_CMD[@]}" '.workloads[] | [.id, .namespace, .kind, .name] | @tsv' "$PLAN_FILE"
}

resource_for_kind() {
  local kind="$1"
  printf '%s' "${kind,,}"
}

kubectl_patch_replicas() {
  local resource="$1"
  local name="$2"
  local namespace="$3"
  local replicas="$4"
  local patch

  patch="$(printf '{"spec":{"replicas":%s}}' "$replicas")"
  if "$DRY_RUN"; then
    echo "DRY-RUN kubectl patch ${resource} ${name} -n ${namespace} --type merge -p '${patch}'"
  else
    kubectl patch "${resource}" "${name}" -n "${namespace}" --type merge -p "${patch}"
  fi
}

kubectl_set_replicas() {
  local kind="$1"
  local resource="$2"
  local name="$3"
  local namespace="$4"
  local replicas="$5"

  case "$kind" in
    Prometheus|Alertmanager)
      kubectl_patch_replicas "$resource" "$name" "$namespace" "$replicas"
      ;;
    *)
      if "$DRY_RUN"; then
        echo "DRY-RUN kubectl scale ${resource} ${name} -n ${namespace} --replicas=${replicas}"
      else
        kubectl scale "${resource}" "${name}" -n "${namespace}" --replicas="${replicas}"
      fi
      ;;
  esac
}

annotate_original() {
  local resource="$1"
  local name="$2"
  local namespace="$3"
  local value="$4"

  if "$DRY_RUN"; then
    echo "DRY-RUN kubectl annotate ${resource}/${name} -n ${namespace} ${ANNOTATION_KEY}=${value} --overwrite"
  else
    kubectl annotate "${resource}/${name}" -n "${namespace}" "${ANNOTATION_KEY}=${value}" --overwrite
  fi
}

clear_annotation() {
  local resource="$1"
  local name="$2"
  local namespace="$3"

  if "$DRY_RUN"; then
    echo "DRY-RUN kubectl annotate ${resource}/${name} -n ${namespace} ${ANNOTATION_KEY}-"
  else
    kubectl annotate "${resource}/${name}" -n "${namespace}" "${ANNOTATION_KEY}-" >/dev/null
  fi
}

get_annotation() {
  local resource="$1"
  local name="$2"
  local namespace="$3"
  local key

  key="${ANNOTATION_KEY//\//\\/}"
  key="${key//./\\.}"
  kubectl get "${resource}/${name}" -n "${namespace}" -o "jsonpath={.metadata.annotations['${key}']}"
}

get_replicas() {
  local resource="$1"
  local name="$2"
  local namespace="$3"

  local replicas
  replicas="$(kubectl get "${resource}/${name}" -n "${namespace}" -o jsonpath='{.spec.replicas}' 2>/dev/null || true)"
  if [[ -z "$replicas" ]]; then
    replicas=1
  fi
  echo "$replicas"
}

case "$ACTION" in
  down)
    while IFS=$'\t' read -r id namespace kind name; do
      resource="$(resource_for_kind "$kind")"
      echo "==> Scaling down ${id} (${kind}/${name} in ${namespace})"
      if ! kubectl get "${resource}/${name}" -n "${namespace}" >/dev/null 2>&1; then
        echo "    WARN: Resource not found, skipping."
        continue
      fi
      current="$(get_replicas "$resource" "$name" "$namespace")"
      stored="$(get_annotation "$resource" "$name" "$namespace")"
      if [[ -z "$stored" ]]; then
        annotate_original "$resource" "$name" "$namespace" "$current"
      else
        echo "    INFO: Using stored original replicas ${stored}"
      fi
      if [[ "$current" -eq 0 ]]; then
        echo "    INFO: Already scaled to 0"
        continue
      fi
      kubectl_set_replicas "$kind" "$resource" "$name" "$namespace" 0
    done < <(read_plan)
    ;;
  up)
    while IFS=$'\t' read -r id namespace kind name; do
      resource="$(resource_for_kind "$kind")"
      echo "==> Restoring ${id} (${kind}/${name} in ${namespace})"
      if ! kubectl get "${resource}/${name}" -n "${namespace}" >/dev/null 2>&1; then
        echo "    WARN: Resource not found, skipping."
        continue
      fi
      stored="$(get_annotation "$resource" "$name" "$namespace")"
      if [[ -z "$stored" ]]; then
        echo "    WARN: No stored replica annotation; skipping restore."
        continue
      fi
      current="$(get_replicas "$resource" "$name" "$namespace")"
      if [[ "$current" -eq "$stored" ]]; then
        echo "    INFO: Already at desired replicas ${stored}"
      else
        kubectl_set_replicas "$kind" "$resource" "$name" "$namespace" "$stored"
      fi
      clear_annotation "$resource" "$name" "$namespace"
    done < <(read_plan)
    ;;
  status)
    printf "%-35s %-12s %-12s %-10s %-10s\n" "ID" "KIND" "NAMESPACE" "REPLICAS" "STORED"
    printf "%-35s %-12s %-12s %-10s %-10s\n" "-----------------------------------" "------------" "------------" "---------" "--------"
    while IFS=$'\t' read -r id namespace kind name; do
      resource="$(resource_for_kind "$kind")"
      if kubectl get "${resource}/${name}" -n "${namespace}" >/dev/null 2>&1; then
        current="$(get_replicas "$resource" "$name" "$namespace")"
        stored="$(get_annotation "$resource" "$name" "$namespace")"
      else
        current="N/A"
        stored="N/A"
      fi
      printf "%-35s %-12s %-12s %-10s %-10s\n" "$id" "$kind" "$namespace" "$current" "${stored:-}"
    done < <(read_plan)
    ;;
  *)
    echo "ERROR: Unknown action '$ACTION'" >&2
    exit 2
    ;;
esac
