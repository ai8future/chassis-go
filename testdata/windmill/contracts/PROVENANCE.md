# Windmill Contract Fixture Provenance

These files are pinned local copies of the Windmill orchestration contracts used by the chassis-go Windmill-readiness test suite.

- Source: `/Users/cliff/Desktop/_code/windmill_suite/windmill_ops/contracts`
- Copied for plan: `.omx/plans/prd-windmill-readiness-20260704T204052Z.md`
- Contract version: `0.1` where declared by the source contracts
- Integrity: see `checksums.sha256` in this directory

Tests must read these repo-local pinned files instead of absolute sibling paths so conformance remains reproducible from the chassis-go worktree alone.
