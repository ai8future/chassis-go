# Missing --version flag on all chassis binaries

**Date**: 2026-03-28
**Severity**: Low (missing foundational feature)

## Problem

Every binary built with chassis-go had no `--version` flag support despite `chassis.Version` being embedded from the VERSION file and `RequireMajor()` being mandatory. There was no way for operators to check what version a binary was running without looking at source code or build artifacts.

## Fix

Added `--version` interception in `RequireMajor()` so it's automatic for every chassis binary with zero consumer code changes. Also added `SetAppVersion()` for consumers who want to include their own version.
