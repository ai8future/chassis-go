# CI missed formatting, security, and release regressions

The previous workflow used an older vulnerable Go patch release, floated its golangci-lint version, combined race and coverage checks without a threshold, and omitted format, module, vulnerability, fuzz, and cross-platform compile gates.

The CI workflow now uses Go 1.26.5 with exactly pinned analysis tools and independently enforces formatting (excluding scratch paths), module reproducibility, vet/static analysis, vulnerability scanning, ordinary and race tests, at least 75% aggregate coverage, bounded fuzzing, and Linux amd64, Darwin arm64, and Linux/386 compile proofs.
