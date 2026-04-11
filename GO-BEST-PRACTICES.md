# Go Best Practices

Prescriptive rules for agents building and maintaining Go services that use chassis-go. Follow these exactly when creating new services or modifying build infrastructure.

---

## 1. Cross-Platform Builds

**Problem:** `make build` produces a binary for the build host's OS/arch only. When developing on Mac (darwin/arm64) and deploying to Linux (linux/amd64), synced binaries fail silently.

**Rule:** Every deployable service must support cross-compilation via `build-linux`, `build-darwin`, and `build-all` Makefile targets.

### Makefile Pattern

```makefile
BINARY := bin/{service_name}

build:
	@rm -f $(BINARY)
	CGO_ENABLED=0 go build $(LDFLAGS) -o $(BINARY) ./cmd/{service_name}

build-linux:
	CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build $(LDFLAGS) -o $(BINARY)-linux-amd64 ./cmd/{service_name}

build-darwin:
	CGO_ENABLED=0 GOOS=darwin GOARCH=arm64 go build $(LDFLAGS) -o $(BINARY)-darwin-arm64 ./cmd/{service_name}

build-all: build-linux build-darwin
	cp scripts/launcher.sh $(BINARY)
	chmod +x $(BINARY)
```

The `@rm -f $(BINARY)` in `build` is required because `go build` refuses to overwrite a non-object file (the launcher script) if `build-all` was run previously.

### Launcher Script

Create `scripts/launcher.sh` — auto-detects platform and execs the correct binary:

```bash
#!/bin/bash
set -e
SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
OS="$(uname -s | tr '[:upper:]' '[:lower:]')"
ARCH="$(uname -m)"
case "$ARCH" in
  x86_64)  ARCH="amd64" ;;
  aarch64) ARCH="arm64" ;;
esac
BINARY="${SCRIPT_DIR}/{service_name}-${OS}-${ARCH}"
if [ ! -f "$BINARY" ]; then
  echo "error: no binary for ${OS}/${ARCH} (expected ${BINARY})" >&2
  exit 1
fi
exec "$BINARY" "$@"
```

Make it executable: `chmod +x scripts/launcher.sh`

### Multi-Binary Variant

For projects with multiple binaries (e.g., rcodegen has 6), create one launcher per binary in `scripts/launcher-{name}.sh` and use a loop in `build-all`:

```makefile
BINS := rclaude rcodex rgemini rcodegen rserve rbatch

build-all: linux darwin
	@for bin in $(BINS); do cp scripts/launcher-$$bin.sh $(BINDIR)/$$bin && chmod +x $(BINDIR)/$$bin; done
```

### Prerequisite

The codebase must be pure Go with `CGO_ENABLED=0` (no C bindings). If CGo is required, cross-compilation needs a cross-compiler toolchain and this pattern won't work as-is.

### Verification

```bash
make build-all
file bin/{service_name}-linux-amd64    # → ELF 64-bit LSB executable
file bin/{service_name}-darwin-arm64   # → Mach-O 64-bit arm64 executable
./bin/{service_name} healthcheck       # launcher picks correct binary
```

**Reference implementation:** `proxy_pool_svc`, `serp_svc`

---

## 2. Makefile Conventions

Every Go service with a Makefile must include these variables and targets.

### Required Variables

```makefile
VERSION := $(shell cat VERSION)
LDFLAGS := -ldflags="-w -s -X main.version=$(VERSION)"
BINARY  := bin/{service_name}
```

### Required Targets

| Target | Purpose |
|--------|---------|
| `build` | Native build with `CGO_ENABLED=0` and `$(LDFLAGS)` |
| `build-linux` | Cross-compile for `linux/amd64` |
| `build-darwin` | Cross-compile for `darwin/arm64` |
| `build-all` | Build both platforms + install launcher script |
| `test` | `go test -v -race -cover ./...` |
| `clean` | `rm -rf bin/` and `rm -rf gen/` |
| `lint` | `golangci-lint run ./...` |
| `deps` | `go mod download` + `go mod tidy` |
| `run` | `build` then execute the binary |

### Required Boilerplate

```makefile
.PHONY: build build-linux build-darwin build-all test clean lint deps run
.DEFAULT_GOAL := build
```

Add `proto` and `docker` targets if the project uses protobuf or Docker.

**Reference implementation:** `proxy_pool_svc/Makefile`

---

## 3. Binary Naming

| Rule | Rationale |
|------|-----------|
| Use the service name as the binary name, never `server` | All services showing as `server` in `ps ax` is indistinguishable |
| Output to `bin/`, never the project root | Keeps the project root clean, matches `.gitignore` conventions |
| `bin/` must be in `.gitignore` | Build artifacts must not be committed |
| Cross-compiled binaries use `{name}-{os}-{arch}` suffix | e.g., `linkd-server-linux-amd64`, `airborne-darwin-arm64` |

---

## 4. VERSION & App Version

Every chassis-go service must provide its app version via `SetAppVersion()`. This enables the automatic `--version` flag and auto-rebuild freshness check (stale binaries recompile themselves).

### Standard Pattern: appversion.go at repo root

Create `appversion.go` next to your `VERSION` and `go.mod`:

```go
// appversion.go
package yourpkg

import (
    _ "embed"
    "strings"
)

//go:embed VERSION
var rawAppVersion string

var AppVersion = strings.TrimSpace(rawAppVersion)
```

Then in every `cmd/*/main.go`:

```go
func main() {
    chassis.SetAppVersion(yourpkg.AppVersion)
    chassis.RequireMajor(11)
    // ...
}
```

This works because `appversion.go` and `VERSION` are in the same directory (repo root), so `go:embed` can access it. Every binary imports the root package — standard Go import, no filesystem tricks.

Do NOT symlink VERSION into `cmd/` directories — `go:embed` rejects symlinks. Do NOT copy VERSION into `cmd/` directories — copies get out of sync.

### Legacy: LDFLAGS

Some projects inject version via linker flags:

```makefile
VERSION := $(shell cat VERSION)
LDFLAGS := -ldflags="-w -s -X main.version=$(VERSION)"
```

This still works for Makefile-driven builds and for injecting additional values (git commit, build time). But LDFLAGS alone does not enable the `--version` flag or auto-rebuild — `SetAppVersion()` is still required. If you use LDFLAGS for other values, you can still use `appversion.go` for the version itself.

---

## 5. Dockerfile Conventions

### Multi-Stage Build

```dockerfile
# Build stage
FROM golang:1.25-alpine AS builder
WORKDIR /app
RUN apk add --no-cache git ca-certificates
COPY go.mod go.sum ./
RUN go mod download
COPY . .
ARG VERSION=dev
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build \
    -ldflags="-w -s -X main.version=${VERSION}" \
    -o /app/{service_name} ./cmd/{service_name}

# Production stage
FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=builder /app/{service_name} /{service_name}
COPY --from=builder /app/VERSION /VERSION
EXPOSE 50051 8080 9090
HEALTHCHECK --interval=30s --timeout=5s --start-period=10s --retries=3 \
    CMD ["/{service_name}", "healthcheck"]
ENTRYPOINT ["/{service_name}"]
```

### Rules

| Rule | Detail |
|------|--------|
| Builder image | `golang:1.25-alpine` |
| Production image | `gcr.io/distroless/static-debian12:nonroot` (preferred) or `alpine:3.21` if shell access needed |
| Always set `CGO_ENABLED=0 GOOS=linux GOARCH=amd64` | Even though Docker runs Linux — makes the build explicit and reproducible |
| Copy VERSION into image | Health check endpoints should report the version |
| Use `nonroot` user | Never run as root in production |
| Standard ports | gRPC: 50051, HTTP: 8080, Admin/Metrics: 9090 (or use `chassis.Port()` for deterministic assignment) |

**Reference implementation:** `linkd_svc/Dockerfile`, `email_svc/Dockerfile`

---

## 6. Error Handling Patterns

_TODO: Document chassis `errors` package usage — ValidationError, TimeoutError, NotFoundError, error wrapping conventions._

## 7. Logging Conventions

_TODO: Document `logz` usage — log levels, structured fields, request-scoped logging, avoiding sensitive data in logs._

## 8. Testing with testkit

_TODO: Document `testkit.NewLogger`, `testkit.SetEnv`, `testkit.GetFreePort`, table-driven test patterns._

## 9. Health Check Composition

_TODO: Document `health.Register`, parallel checks, partial failure handling, health check HTTP/gRPC endpoints._

## 10. HTTP Client / call.Client Patterns

_TODO: Document retry configuration, circuit breaker tuning, timeout defaults, OTel span propagation._
