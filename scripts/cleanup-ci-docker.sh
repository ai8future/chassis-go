#!/usr/bin/env bash
set -uo pipefail

if (( $# < 1 || $# > 2 )); then
  echo "usage: $0 <artifact-dir> [log-tail-lines]" >&2
  exit 2
fi

out="$1"
tail_lines="${2:-500}"
mkdir -p "$out" || exit 1
cleanup_log="$out/cleanup.txt"
cleanup_status=0

if ! docker ps -a >"$out/docker-ps.txt" 2>&1; then
  printf 'docker_inventory_failed=ps\n' >>"$cleanup_log"
  cleanup_status=1
fi
docker images --digests >"$out/docker-images.txt" 2>&1 || true

containers="$(docker ps -a --format '{{.Names}}' 2>>"$cleanup_log")"
inventory_status=$?
if (( inventory_status != 0 )); then
  printf 'docker_inventory_failed=names status=%d\n' "$inventory_status" >>"$cleanup_log"
  cleanup_status=1
else
  while IFS= read -r container; do
    [[ "$container" == chassis-* ]] || continue
    safe="${container//[^A-Za-z0-9_.-]/_}"
    docker logs --tail "$tail_lines" "$container" >"$out/${safe}.logs.txt" 2>&1 || true
    docker inspect "$container" >"$out/${safe}.inspect.json" 2>&1 || true
    if ! docker rm -f -v "$container" >>"$cleanup_log" 2>&1; then
      printf 'container_cleanup_failed=%s\n' "$container" >>"$cleanup_log"
      cleanup_status=1
    fi
  done <<<"$containers"
fi

timestamp="$(date -u +%Y-%m-%dT%H:%M:%SZ)"
if (( cleanup_status == 0 )); then
  printf 'cleanup_complete=%s\n' "$timestamp" >>"$cleanup_log"
else
  printf 'cleanup_failed=%s\n' "$timestamp" >>"$cleanup_log"
fi
exit "$cleanup_status"
