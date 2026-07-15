#!/usr/bin/env bash
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$repo_root"
registry="${CHASSIS_INTEGRATION_REGISTRY:-testing/integration-suites.tsv}"
image_registry="${CHASSIS_INTEGRATION_IMAGES:-testing/integration-images.tsv}"
requested="${1:-}"

if [[ -z "$requested" ]]; then
  echo "usage: $0 <service|all>" >&2
  exit 2
fi
if [[ ! -f "$registry" ]]; then
  echo "integration suite registry not found: $registry" >&2
  exit 2
fi
if [[ ! -f "$image_registry" ]]; then
  echo "integration image registry not found: $image_registry" >&2
  exit 2
fi

services=()
packages=()
image_services=()
image_refs=()
while IFS=$'\t' read -r service package extra; do
  [[ -z "$service" || "${service:0:1}" == '#' ]] && continue
  if [[ -z "$package" || -n "${extra:-}" ]]; then
    echo "invalid integration registry row for $service" >&2
    exit 2
  fi
  services+=("$service")
  packages+=("$package")
done < "$registry"

while IFS=$'	' read -r service image amd64_manifest arm64_manifest source extra; do
  [[ -z "$service" || "${service:0:1}" == "#" ]] && continue
  if [[ -z "$image" || -z "$amd64_manifest" || -z "$arm64_manifest" || -z "$source" || -n "${extra:-}" ]]; then
    echo "invalid integration image registry row for $service" >&2
    exit 2
  fi
  if [[ "$image" != *@sha256:* || "$image" == *:latest* ]]; then
    echo "integration image for $service is not an immutable non-latest digest pin: $image" >&2
    exit 2
  fi
  if [[ "$amd64_manifest" != sha256:* || "$arm64_manifest" != sha256:* ]]; then
    echo "integration image for $service is missing per-arch manifest digests" >&2
    exit 2
  fi
  image_services+=("$service")
  image_refs+=("$image")
done < "$image_registry"

image_for_service() {
  local requested_service="$1"
  local found=""
  for j in "${!image_services[@]}"; do
    if [[ "${image_services[$j]}" == "$requested_service" ]]; then
      if [[ -n "$found" ]]; then
        echo "duplicate integration image pin for $requested_service" >&2
        return 2
      fi
      found="${image_refs[$j]}"
    fi
  done
  if [[ -z "$found" ]]; then
    echo "registered integration suite $requested_service has no pinned image entry" >&2
    return 2
  fi
  printf '%s' "$found"
}

selected=()
if [[ "$requested" == all ]]; then
  if (( ${#services[@]} == 0 )); then
    echo 'no live integration suites are registered; refusing an empty success' >&2
    exit 2
  fi
  for i in "${!services[@]}"; do selected+=("$i"); done
else
  for i in "${!services[@]}"; do
    [[ "${services[$i]}" == "$requested" ]] && selected+=("$i")
  done
  if (( ${#selected[@]} == 0 )); then
    echo "unknown integration service: $requested" >&2
    exit 2
  fi
fi

marker_dir="$(mktemp -d)"
trap 'rm -rf "$marker_dir"' EXIT
for i in "${selected[@]}"; do
  service="${services[$i]}"
  package="${packages[$i]}"
  image="$(image_for_service "$service")"
  echo "running selected integration suite: $service ($package)"
  echo "using pinned integration image: $service $image"
  CHASSIS_INTEGRATION_SERVICES="$service" \
  CHASSIS_INTEGRATION_MARKER_DIR="$marker_dir" \
    go test -timeout="${CHASSIS_INTEGRATION_TIMEOUT:-120s}" -count=1 -tags=integration -json "$package"
  marker="$marker_dir/$service.complete"
  if [[ ! -f "$marker" ]] || [[ "$(cat "$marker")" != "$service" ]]; then
    echo "selected integration suite $service produced no valid completion marker" >&2
    exit 1
  fi
done
