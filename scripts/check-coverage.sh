#!/usr/bin/env bash
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$repo_root"
profile="$(mktemp)"
trap 'rm -f "$profile"' EXIT

go test -timeout=120s -count=1 -coverprofile="$profile" ./...
go run ./internal/cmd/checkcoverage -profile "$profile" -policy testing/coverage-policy.json
