#!/usr/bin/env bash
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$repo_root"

echo 'T1 E2E: clikit subprocess/generated-consumer plus process/TCP/Docker matrix (Docker skips only when unavailable).'
go test -timeout=120s -count=1 ./clikit -run '^(TestExampleSmokeBuildsAndRuns|TestConsumerFixtureVersionJSONFailuresSignalAndFreshness)$'
go test -timeout=10m -count=1 -tags=e2e ./e2e
