# Test-owned anonymous Docker volume leaks

Exact owned containers were removed with `docker rm -f`, which left image-declared anonymous volumes behind while cleanup reported success. Shared integration cleanup, the nightly owner, and hosted CI cleanup now remove attached anonymous volumes with the exact container via `docker rm -f -v`; no global prune or volume-name heuristic is used.

Argument-level regressions cover all three cleanup surfaces. The selected pinned Redpanda suite also records preflight volume IDs, verifies its newly attached anonymous volume, removes the exact container, and fails unless the volume inventory returns exactly to preflight.
