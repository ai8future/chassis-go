# registry: resolveName() diverged from lifecycle, broke 39 tests

**Date:** 2026-04-11
**Severity:** High (all registry + lifecycle integration tests broken)
**Packages:** registry, lifecycle

## Root cause

`registry.resolveName()` used `filepath.Base(os.Args[0]) + "-" + filepath.Base(wd)` (e.g., `registry.test-registry`) while `lifecycle.resolveName()` used just `filepath.Base(wd)` (e.g., `registry`). The lifecycle comment said "mirrors the logic in the registry package" but they diverged.

All tests computed expected PID file paths using `filepath.Base(wd)`, but the registry created files in a directory named `binary-cwd`. Tests always failed to find their own PID files.

## Fix

Changed `registry.resolveName()` to use `filepath.Base(wd)`, matching lifecycle. Extracted `testSvcDir` helper in registry_test.go to replace 16 inline broken path computations.
