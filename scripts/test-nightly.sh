#!/usr/bin/env bash
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$repo_root"

echo 'T3 baseline only: extended deterministic fuzz smoke; live restart/repetition scenarios are not implemented in G001.'
go test ./seal -run='^$' -fuzz='^FuzzDecryptEnvelopeNeverPanics$' -fuzztime="${CHASSIS_FUZZTIME:-30s}"
go test ./webhook -run='^$' -fuzz='^FuzzVerifyWebhookNeverPanics$' -fuzztime="${CHASSIS_FUZZTIME:-30s}"
