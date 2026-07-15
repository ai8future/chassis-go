#!/usr/bin/env bash
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$repo_root"

if (( $# > 1 )); then
  echo "usage: $0 [nightly-producer]" >&2
  exit 2
fi

artifact_root="${CHASSIS_NIGHTLY_CI_ARTIFACT_ROOT:-artifacts}"
producer="${1:-./scripts/test-nightly.sh}"
mkdir -p "$artifact_root/nightly"

set +e
"$producer" 2>&1 | tee "$artifact_root/nightly.log"
pipeline_status=("${PIPESTATUS[@]}")
set -e

producer_status="${pipeline_status[0]}"
tee_status="${pipeline_status[1]}"
if (( producer_status != 0 )); then
  exit "$producer_status"
fi
exit "$tee_status"
