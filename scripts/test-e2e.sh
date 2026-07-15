#!/usr/bin/env bash
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$repo_root"

case "${CHASSIS_E2E_DOCKER_REQUIRED:-0}" in
  0|'') docker_mode='optional local Docker evidence; unavailability is reported as a skip' ;;
  1) docker_mode='required Docker evidence; CLI or daemon unavailability fails closed' ;;
  *) echo 'CHASSIS_E2E_DOCKER_REQUIRED must be 0 or 1' >&2; exit 2 ;;
esac

echo "T1 E2E: clikit subprocess/generated-consumer plus process/TCP/Docker matrix ($docker_mode)."
go test -timeout=120s -count=1 ./clikit -run '^(TestExampleSmokeBuildsAndRuns|TestConsumerFixtureVersionJSONFailuresSignalAndFreshness)$'
go test -timeout=10m -count=1 -tags=e2e ./e2e
