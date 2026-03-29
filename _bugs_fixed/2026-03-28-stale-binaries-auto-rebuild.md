# Stale binaries running outdated versions

**Date**: 2026-03-28
**Severity**: Medium (operational correctness)

## Problem

Binaries frequently ran outdated code because VERSION was updated but the binary wasn't recompiled. No mechanism existed to detect or remedy this at runtime despite source always being shipped alongside binaries.

## Fix

Added auto-rebuild freshness check in `RequireMajor()`. When `SetAppVersion()` is called, the framework compares the compiled-in version against the VERSION file on disk. If the disk version is newer, it automatically runs `go build` (atomic rename to temp file), then re-execs via `syscall.Exec`. Opt out with `CHASSIS_NO_REBUILD=1`. Loop guard via `CHASSIS_REBUILD_GUARD`.
