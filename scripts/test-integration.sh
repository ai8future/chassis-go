#!/usr/bin/env bash
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$repo_root"
registry="${CHASSIS_INTEGRATION_REGISTRY:-testing/integration-suites.tsv}"
requested="${1:-}"

if [[ -z "$requested" ]]; then
  echo "usage: $0 <service|all>" >&2
  exit 2
fi
if [[ ! -f "$registry" ]]; then
  echo "integration suite registry not found: $registry" >&2
  exit 2
fi

services=()
packages=()
while IFS=$'\t' read -r service package extra; do
  [[ -z "$service" || "${service:0:1}" == '#' ]] && continue
  if [[ -z "$package" || -n "${extra:-}" ]]; then
    echo "invalid integration registry row for $service" >&2
    exit 2
  fi
  services+=("$service")
  packages+=("$package")
done < "$registry"

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
  echo "running selected integration suite: $service ($package)"
  CHASSIS_INTEGRATION_SERVICES="$service" \
  CHASSIS_INTEGRATION_MARKER_DIR="$marker_dir" \
    go test -timeout="${CHASSIS_INTEGRATION_TIMEOUT:-120s}" -count=1 -tags=integration -json "$package"
  marker="$marker_dir/$service.complete"
  if [[ ! -f "$marker" ]] || [[ "$(cat "$marker")" != "$service" ]]; then
    echo "selected integration suite $service produced no valid completion marker" >&2
    exit 1
  fi
done
