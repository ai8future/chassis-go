# phasekit - Phase Secrets Integration

`phasekit` hydrates environment variables from Phase before `config.MustLoad`
runs. It is a startup bridge: it calls the `phase` CLI, reads JSON, and applies
the returned key/value pairs to `os.Environ`.

Use this when your service already uses chassis `config.MustLoad` but wants
Phase to hold secrets centrally with audit history, role-based access, and
rotation workflows. Typed config still lives in your Go structs; Phase only
provides the environment values those structs read.

## Requirements

- Phase CLI 2.2.0 or newer installed in the runtime image
- `PHASE_SERVICE_TOKEN` available to the process
- Optional `PHASE_HOST` for self-hosted Phase
- `chassis.RequireMajor(11)` called before `phasekit.Hydrate` or
  `phasekit.MustHydrate`

`phasekit` adds no Go module dependency on the Phase SDK. Runtime images do
need the external `phase` binary.

## Quickstart

```go
package main

import (
    "context"
    "log"
    "os"

    chassis "github.com/ai8future/chassis-go/v11"
    "github.com/ai8future/chassis-go/v11/config"
    "github.com/ai8future/chassis-go/v11/phasekit"

    "github.com/example/myservice"
)

func main() {
    chassis.SetAppVersion(myservice.AppVersion)
    chassis.RequireMajor(11)

    ctx := context.Background()
    phasekit.MustHydrate(ctx, phasekit.Config{
        ServiceToken: os.Getenv("PHASE_SERVICE_TOKEN"),
        Host:         os.Getenv("PHASE_HOST"),
        App:          "myservice",
        Env:          envOr("APP_ENV", "Production"),
        RequiredKeys: []string{"DATABASE_URL", "JWT_SIGNING_KEY"},
    })

    cfg := config.MustLoad[myservice.Config]()
    if err := myservice.Run(ctx, cfg); err != nil {
        log.Fatal(err)
    }
}

func envOr(key, fallback string) string {
    if v := os.Getenv(key); v != "" {
        return v
    }
    return fallback
}
```

Existing environment variables win by default. That means local `.env` files,
shell exports, Kubernetes secrets, and CI secrets can override Phase values.
Set `OverwriteExisting: true` only when Phase should replace values that are
already present in the process environment.

## Path Semantics

Phase path filtering is exact-match:

| Config | CLI path | Result |
|---|---|---|
| zero value | `--path /` | root path only |
| `Path: "/db"` | `--path /db` | only `/db` secrets |
| `AllPaths: true` | `--path ""` | all paths |

There is no recursive subtree mode in v1. To fetch every path, use
`AllPaths: true`.

## Dynamic Secrets

Phase CLI `secrets export` can generate dynamic secret leases by default.
`phasekit` v1 always passes `--generate-leases=false` because startup hydration
does not include lease renewal or revocation.

If your service needs dynamic secrets, do not rely on phasekit v1 startup
hydration. Add a lease lifecycle design first.

## AI Redaction

Phase can redact exported values when AI-agent mode is enabled. `phasekit`
rejects literal `[REDACTED]` values by default so the service fails at startup
instead of running with placeholder secrets.

If you intentionally test redaction behavior, set `AllowRedacted: true`.
Production services should leave it false.

## Docker

Alpine example:

```dockerfile
FROM golang:1.26-alpine AS builder
WORKDIR /src
COPY . .
RUN go build -o /out/myservice ./cmd/myservice

FROM alpine:3.20
RUN apk add --no-cache curl ca-certificates \
 && curl -fsSL -o /usr/local/bin/phase \
      https://github.com/phasehq/cli/releases/download/v2.2.0/phase_cli_2.2.0_linux_amd64 \
 && chmod +x /usr/local/bin/phase \
 && apk del curl

COPY --from=builder /out/myservice /usr/local/bin/myservice
ENV PHASE_HOST=https://phase.example.com
ENTRYPOINT ["/usr/local/bin/myservice"]
```

Verify the Phase release asset name for your target version, operating system,
and architecture. For distroless images, copy the `phase` binary from a builder
stage instead of installing curl in the final image.

## CI

Store `PHASE_SERVICE_TOKEN` in the CI secret store. For self-hosted Phase, also
set `PHASE_HOST`.

Do not print the token, and do not pass it as a command-line argument. Phasekit
passes it through the subprocess environment so it is not visible in `ps`
output.

## Troubleshooting

| Symptom | Likely cause | Fix |
|---|---|---|
| `phasekit: phase binary not found in PATH` | Runtime image lacks the CLI | Install or copy the `phase` binary |
| `phase CLI exited ... 403` | Invalid or expired token | Rotate `PHASE_SERVICE_TOKEN` |
| `required keys missing` | Secret absent from the configured app/env/path | Check `App`, `Env`, `Path`, and `AllPaths` |
| `returned redacted value` | Phase AI redaction is active | Disable Phase AI mode or remove `~/.phase/ai.json` |
| Existing env value was not replaced | Default preservation behavior | Set `OverwriteExisting: true` if Phase should win |

## Security Notes

- `PHASE_SERVICE_TOKEN` remains a bootstrap secret. Store it in the platform's
  normal secret store.
- Environment variables can be visible through process inspection on some
  systems, such as `/proc/PID/environ`. This is the same tradeoff as other
  env-based secret bootstraps.
- The Phase subprocess receives only `PHASE_SERVICE_TOKEN`, `PHASE_HOST`, and a
  small proxy/TLS allowlist. It does not inherit the full service environment.
- Phase secret names are not logged. Phasekit logs only set/skipped counts.

## Manual Smoke Test

1. Install Phase CLI 2.2.0 or newer.
2. Export `PHASE_SERVICE_TOKEN` and, for self-hosted Phase, `PHASE_HOST`.
3. Wire a small service with `phasekit.MustHydrate` before `config.MustLoad`.
4. Verify required keys hydrate successfully.
5. Pre-set one key locally and verify the local value wins.
6. Set `OverwriteExisting: true` and verify the Phase value wins.
7. Add a missing `RequiredKeys` entry and verify startup fails cleanly.
