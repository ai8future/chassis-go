# Nightly package enumeration false green

Root cause: `scripts/test-nightly.sh` streamed `go list ./...` through process substitution, so Bash did not propagate a nonzero package-enumeration exit after partial package output.

Fix: package enumeration now runs as an explicit precondition. `go list` stderr remains visible, and any enumeration failure returns before fuzz target discovery or completion logging.

Regression: added a fake-`go` topology test where `go list ./...` emits `./good`, prints an error, and exits nonzero. The test asserts the nightly script fails, preserves the error and diagnostic, and never reports fuzz completion.
