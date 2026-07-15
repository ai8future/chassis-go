#!/usr/bin/env bash
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$repo_root"

artifact_dir="${CHASSIS_NIGHTLY_ARTIFACT_DIR:-artifacts/nightly-$(date -u +%Y%m%dT%H%M%SZ)}"
if [[ "$artifact_dir" != /* ]]; then
  artifact_dir="$repo_root/$artifact_dir"
fi
mkdir -p "$artifact_dir"
summary="$artifact_dir/summary.txt"
: >"$summary"
active_containers=()

log() {
  printf '[nightly] %s\n' "$*" | tee -a "$summary"
}

free_port() {
  python3 - <<'PY'
import socket
s = socket.socket()
s.bind(('127.0.0.1', 0))
print(s.getsockname()[1])
s.close()
PY
}

image_for_service() {
  local requested="$1"
  local found=0
  local image=""
  while IFS=$'\t' read -r service candidate amd64_manifest arm64_manifest source extra; do
    [[ -z "${service:-}" || "${service:0:1}" == '#' ]] && continue
    if [[ -z "${candidate:-}" || -z "${amd64_manifest:-}" || -z "${arm64_manifest:-}" || -z "${source:-}" || -n "${extra:-}" ]]; then
      printf 'invalid integration image registry row for %s\n' "$service" >&2
      return 2
    fi
    if [[ "$service" != "$requested" ]]; then
      continue
    fi
    found=$((found + 1))
    image="$candidate"
    if [[ "$image" != *@sha256:* || "$image" == *:latest* ]]; then
      printf 'nightly image for %s is not an immutable non-latest digest pin: %s\n' "$service" "$image" >&2
      return 2
    fi
    if [[ "$amd64_manifest" != sha256:* || "$arm64_manifest" != sha256:* ]]; then
      printf 'nightly image for %s is missing per-arch manifest digests\n' "$service" >&2
      return 2
    fi
    printf 'service=%s\nimage=%s\namd64_manifest=%s\narm64_manifest=%s\nsource=%s\n' \
      "$service" "$image" "$amd64_manifest" "$arm64_manifest" "$source" >"$artifact_dir/${service}-image.txt"
  done < testing/integration-images.tsv
  if (( found != 1 )); then
    printf 'expected exactly one pinned image for %s, found %d\n' "$requested" "$found" >&2
    return 2
  fi
  printf '%s' "$image"
}

collect_container_diagnostics() {
  local reason="$1"
  for container in "${active_containers[@]+${active_containers[@]}}"; do
    [[ -z "$container" ]] && continue
    local safe="${container//[^A-Za-z0-9_.-]/_}"
    docker logs --tail 500 "$container" >"$artifact_dir/${safe}.${reason}.logs.txt" 2>&1 || true
    docker inspect "$container" >"$artifact_dir/${safe}.${reason}.inspect.json" 2>&1 || true
  done
}

cleanup_containers() {
  local rc="$1"
  if (( rc != 0 )); then
    collect_container_diagnostics failure
  fi
  for container in "${active_containers[@]+${active_containers[@]}}"; do
    [[ -z "$container" ]] && continue
    docker rm -f "$container" >>"$artifact_dir/container-cleanup.txt" 2>&1 || true
  done
  if [[ -d "$artifact_dir/otel-receipts" ]]; then
    find "$artifact_dir/otel-receipts" -mindepth 1 -maxdepth 1 -type d -exec chmod 0755 {} +
  fi
}

on_exit() {
  local rc=$?
  cleanup_containers "$rc"
  log "nightly exit status: $rc"
  exit "$rc"
}
trap on_exit EXIT

run_logged() {
  local name="$1"
  shift
  local logfile="$artifact_dir/${name//[^A-Za-z0-9_.-]/_}.log"
  local started finished status
  started="$(date -u +%Y-%m-%dT%H:%M:%SZ)"
  log "START $name at $started: $*"
  set +e
  "$@" 2>&1 | tee "$logfile"
  status="${PIPESTATUS[0]}"
  set -e
  finished="$(date -u +%Y-%m-%dT%H:%M:%SZ)"
  log "END $name at $finished status=$status log=$logfile"
  return "$status"
}

wait_http() {
  local name="$1"
  local url="$2"
  local timeout_seconds="$3"
  local contains="${4:-}"
  local deadline=$((SECONDS + timeout_seconds))
  local last="not attempted"
  while (( SECONDS < deadline )); do
    set +e
    body="$(curl -fsS --max-time 5 "$url" 2>&1)"
    status=$?
    set -e
    if (( status == 0 )); then
      if [[ -z "$contains" || "$body" == *"$contains"* ]]; then
        printf '%s readiness ok url=%s duration_seconds=%s\n' "$name" "$url" "$((timeout_seconds - (deadline - SECONDS)))" | tee -a "$summary"
        return 0
      fi
      last="$body"
    else
      last="$body"
    fi
    sleep 2
  done
  printf '%s readiness timed out url=%s last=%s\n' "$name" "$url" "$last" >&2
  return 1
}

assert_docker() {
  docker version >"$artifact_dir/docker-version.txt" 2>&1
}

run_fuzz_targets() {
  local fuzztime="${CHASSIS_FUZZTIME:-60s}"
  local discovered=0
  while IFS= read -r package; do
    [[ -z "$package" ]] && continue
    while IFS= read -r fuzz; do
      [[ -z "$fuzz" ]] && continue
      discovered=$((discovered + 1))
      run_logged "fuzz-${package##*/}-${fuzz}" go test "$package" -run '^$' -fuzz="^${fuzz}$" -fuzztime="$fuzztime"
    done < <(go test "$package" -run '^$' -list '^Fuzz' 2>/dev/null | awk '/^Fuzz/ {print $1}')
  done < <(go list ./...)
  if (( discovered == 0 )); then
    printf 'no fuzz targets discovered; refusing false-green nightly fuzz\n' >&2
    return 1
  fi
  log "fuzz targets completed: $discovered with fuzztime=$fuzztime"
}

run_race_repetitions() {
  local count="${CHASSIS_NIGHTLY_RACE_COUNT:-3}"
  read -r -a packages <<<"${CHASSIS_NIGHTLY_RACE_PACKAGES:-./lifecycle ./work ./kafkakit}"
  for package in "${packages[@]}"; do
    [[ -z "$package" ]] && continue
    run_logged "race-${package//\//_}" go test -race -count="$count" -timeout=240s "$package"
  done
}

new_otel_receipts_dir() {
  local label="$1" root="$artifact_dir/otel-receipts" dir
  mkdir -p "$root"
  dir="$(mktemp -d "$root/${label}.XXXXXX")"
  chmod 0777 "$dir"
  printf '%s' "$dir"
}

assert_otel_receipts() {
  local dir="$1" receipt
  for receipt in traces.json metrics.json receipt.json; do
    if [[ ! -s "$dir/$receipt" ]]; then
      printf 'otel integration receipt is missing or empty: %s\n' "$dir/$receipt" >&2
      return 1
    fi
  done
  log "otel integration receipts retained: $dir"
}

run_integration_repetitions() {
  local selected="${CHASSIS_NIGHTLY_INTEGRATIONS:-all}"
  local count="${CHASSIS_NIGHTLY_INTEGRATION_COUNT:-2}"
  if [[ "$selected" == "none" ]]; then
    log 'integration repetitions disabled by CHASSIS_NIGHTLY_INTEGRATIONS=none'
    return 0
  fi
  local i receipts_dir
  for ((i = 1; i <= count; i++)); do
    if [[ "$selected" == "all" || "$selected" == "otel-collector" ]]; then
      receipts_dir="$(new_otel_receipts_dir "integration-${i}")"
      run_logged "integration-${selected}-repeat-${i}" env \
        CHASSIS_OTEL_RECEIPT_DIR="$receipts_dir" \
        ./scripts/test-integration.sh "$selected"
      assert_otel_receipts "$receipts_dir"
    else
      run_logged "integration-${selected}-repeat-${i}" ./scripts/test-integration.sh "$selected"
    fi
  done
}

restart_probe_qdrant() {
  local image="$1" name="chassis-nightly-qdrant-$$" port
  port="$(free_port)"
  log "starting restart probe service=qdrant image=$image container=$name"
  docker run -d --name "$name" --pull=missing -p "127.0.0.1:${port}:6333" "$image" >/dev/null
  active_containers+=("$name")
  wait_http qdrant "http://127.0.0.1:${port}/collections" 60
  docker restart "$name" >"$artifact_dir/${name}.restart.txt"
  wait_http qdrant "http://127.0.0.1:${port}/collections" 60
  log "restart probe complete: qdrant container=$name"
}

restart_probe_meilisearch() {
  local image="$1" name="chassis-nightly-meili-$$" port
  port="$(free_port)"
  log "starting restart probe service=meilisearch image=$image container=$name"
  docker run -d --name "$name" --pull=missing -e MEILI_NO_ANALYTICS=true -p "127.0.0.1:${port}:7700" "$image" >/dev/null
  active_containers+=("$name")
  wait_http meilisearch "http://127.0.0.1:${port}/health" 60 available
  docker restart "$name" >"$artifact_dir/${name}.restart.txt"
  wait_http meilisearch "http://127.0.0.1:${port}/health" 60 available
  log "restart probe complete: meilisearch container=$name"
}

restart_probe_otel_collector() {
  local image="$1" name="chassis-nightly-otel-$$" otlp_port health_port receipts_dir config_path
  otlp_port="$(free_port)"
  health_port="$(free_port)"
  receipts_dir="$(new_otel_receipts_dir restart)"
  config_path="$repo_root/otel/testdata/collector-config.yaml"
  log "starting restart probe service=otel-collector image=$image container=$name"
  docker run -d --name "$name" --pull=missing \
    -p "127.0.0.1:${otlp_port}:4317" \
    -p "127.0.0.1:${health_port}:13133" \
    -v "${config_path}:/etc/otelcol-contrib/config.yaml:ro" \
    -v "${receipts_dir}:/receipts" \
    "$image" \
    --config=/etc/otelcol-contrib/config.yaml >/dev/null
  active_containers+=("$name")
  wait_http otel-collector "http://127.0.0.1:${health_port}/" 45
  docker restart "$name" >"$artifact_dir/${name}.restart.txt"
  wait_http otel-collector "http://127.0.0.1:${health_port}/" 45
  log "restart probe complete: otel-collector container=$name"
}

restart_probe_inngest() {
  local image="$1" name="chassis-nightly-inngest-$$" port
  port="$(free_port)"
  log "starting restart probe service=inngest image=$image container=$name"
  docker run -d --name "$name" --pull=missing -p "127.0.0.1:${port}:8288" \
    "$image" inngest dev --no-discovery --host 0.0.0.0 >/dev/null
  active_containers+=("$name")
  wait_http inngest "http://127.0.0.1:${port}/" 45 Inngest
  docker restart "$name" >"$artifact_dir/${name}.restart.txt"
  wait_http inngest "http://127.0.0.1:${port}/" 45 Inngest
  log "restart probe complete: inngest container=$name"
}

restart_probe_redpanda() {
  local image="$1" name="chassis-nightly-redpanda-$$" kafka_port schema_port admin_port
  kafka_port="$(free_port)"
  schema_port="$(free_port)"
  admin_port="$(free_port)"
  log "starting restart probe service=redpanda image=$image container=$name"
  docker run -d --name "$name" --pull=missing \
    -p "127.0.0.1:${kafka_port}:19092" \
    -p "127.0.0.1:${schema_port}:18081" \
    -p "127.0.0.1:${admin_port}:9644" \
    "$image" \
    redpanda start \
    --kafka-addr internal://0.0.0.0:9092,external://0.0.0.0:19092 \
    --advertise-kafka-addr "internal://127.0.0.1:9092,external://127.0.0.1:${kafka_port}" \
    --pandaproxy-addr internal://0.0.0.0:8082,external://0.0.0.0:18082 \
    --advertise-pandaproxy-addr internal://127.0.0.1:8082,external://127.0.0.1:18082 \
    --schema-registry-addr internal://0.0.0.0:8081,external://0.0.0.0:18081 \
    --rpc-addr 0.0.0.0:33145 \
    --advertise-rpc-addr 127.0.0.1:33145 \
    --mode dev-container \
    --smp 1 \
    --default-log-level=info >/dev/null
  active_containers+=("$name")
  wait_http redpanda "http://127.0.0.1:${admin_port}/v1/status/ready" 90
  docker restart "$name" >"$artifact_dir/${name}.restart.txt"
  wait_http redpanda "http://127.0.0.1:${admin_port}/v1/status/ready" 90
  log "restart probe complete: redpanda container=$name"
}

run_restart_probe() {
  local service="$1" image
  image="$(image_for_service "$service")"
  case "$service" in
    redpanda) restart_probe_redpanda "$image" ;;
    qdrant) restart_probe_qdrant "$image" ;;
    meilisearch) restart_probe_meilisearch "$image" ;;
    otel-collector) restart_probe_otel_collector "$image" ;;
    inngest) restart_probe_inngest "$image" ;;
    *) printf 'unknown restart probe service: %s\n' "$service" >&2; return 2 ;;
  esac
}

run_restart_probes() {
  local selected="${CHASSIS_NIGHTLY_RESTART_SERVICES:-redpanda qdrant meilisearch otel-collector inngest}"
  if [[ "$selected" == "none" ]]; then
    log 'restart probes disabled by CHASSIS_NIGHTLY_RESTART_SERVICES=none'
    return 0
  fi
  assert_docker
  read -r -a services <<<"$selected"
  for service in "${services[@]}"; do
    [[ -z "$service" ]] && continue
    run_restart_probe "$service"
  done
}

log "nightly artifact dir: $artifact_dir"
run_fuzz_targets
run_race_repetitions
run_integration_repetitions
run_restart_probes
log 'nightly suite complete: fuzz, race repetitions, integration repetitions, and real restart probes finished'
