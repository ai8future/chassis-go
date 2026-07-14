#!/usr/bin/env bash
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$repo_root"

echo 'T1 baseline only: clikit subprocess and generated-consumer checks; full binary/TCP/Docker matrix is not implemented in G001.'
go test -timeout=120s -count=1 ./clikit -run '^(TestExampleSmokeBuildsAndRuns|TestConsumerFixtureVersionJSONFailuresSignalAndFreshness)$'
