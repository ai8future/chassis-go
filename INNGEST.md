# inngestkit — Durable Workflow Integration

`inngestkit` provides thin setup glue for wiring [inngest's Go SDK](https://github.com/inngest/inngestgo) into a chassis service. It handles config, startup validation, client construction, HTTP mount, and event sending.

**inngestkit does NOT wrap** inngest's function creation, step definitions, retry policies, concurrency controls, or any other SDK feature. Use `inngestgo` directly for those. When inngest adds new features, inngestkit does not need to update.

## When to use

Use inngestkit when your service needs:
- Durable multi-step workflows with automatic retry and state preservation
- Event-driven pipelines (wait-for-event, debounce, fanout)
- Code-defined background jobs versioned with the application
- Webhook processing with retry
- Scheduled tasks beyond OS-level cron

## When NOT to use

- Services without durable workflow needs should not integrate inngestkit.
- inngestkit is opt-in, not required for service completion.
- OS-level cron is fine when OS-level cron is sufficient.

## Integration (4 lines)

```go
import (
    "github.com/ai8future/chassis-go/v11/inngestkit"
    "github.com/inngest/inngestgo"
)

type Config struct {
    LogLevel string            `env:"LOG_LEVEL" default:"info"`
    Port     int               `env:"PORT"      default:"8080"`
    Inngest  inngestkit.Config // populated from INNGEST_* env vars
}

func main() {
    chassis.SetAppVersion(yourpkg.AppVersion)
    chassis.RequireMajor(11)

    cfg := config.MustLoad[Config]()

    // 1. Create the kit
    kit, err := inngestkit.New(cfg.Inngest)
    if err != nil {
        log.Fatal(err)
    }

    // 2. Define functions using the native SDK
    processSignup, _ := inngestgo.CreateFunction(
        kit.Client(),
        inngestgo.FunctionOpts{ID: "process-signup", Name: "Process Signup"},
        inngestgo.EventTrigger("user/signup", nil),
        func(ctx context.Context, in inngestgo.Input[SignupEvent]) (any, error) {
            // Use native SDK steps directly
            return nil, nil
        },
    )

    // 3. Mount the serve handler
    mux := http.NewServeMux()
    kit.Mount(mux)

    // 4. Send events
    kit.Send(ctx, inngestgo.Event{
        Name: "user/signup",
        Data: map[string]any{"email": "user@example.com"},
    })

    lifecycle.Run(ctx, srv.ListenAndServe)
}
```

## Environment variables

| Variable | Required | Default | Purpose |
|---|---|---|---|
| `INNGEST_BASE_URL` | yes | — | Self-hosted inngest server (e.g. `http://inngest.lan:8288`) |
| `INNGEST_APP_ID` | yes | — | Stable app identity, typically matches service name |
| `INNGEST_EVENT_KEY` | yes | — | Authenticates Send calls (app -> inngest) |
| `INNGEST_SIGNING_KEY` | yes | — | Verifies callback signatures (inngest -> app). Must be at least 32 hex chars. Generate with `openssl rand -hex 32`. Accepts both raw hex and the full prefixed form (`signkey-prod-<hex>`) — the prefix is stripped automatically. |
| `INNGEST_SIGNING_KEY_FALLBACK` | no | — | Previous signing key for zero-downtime rotation. Same format rules as `INNGEST_SIGNING_KEY`. |
| `INNGEST_SERVE_ORIGIN` | no | — | App's own URL for inngest callbacks (e.g. `http://myservice.lan:8080`). Must start with `http://` or `https://`. If unset, SDK infers from incoming requests. Set when behind a reverse proxy or when the app's external URL differs from what it sees. |
| `INNGEST_SERVE_PATH` | no | `/api/inngest` | Path where the serve handler is mounted |

All required fields are required. There is no dev-mode fallback. If a required field is missing, `inngestkit.New` fails at startup.

## Startup validation

`New()` checks at construction time:
1. `BaseURL` is present and starts with `http://` or `https://`
2. `AppID` is present and non-empty
3. `EventKey` is present and non-empty
4. `SigningKey` is present, non-empty, at least 32 hex chars, and valid hex (any `signkey-<env>-` prefix is stripped first)
5. `SigningKeyFallback`, if present, also meets the same hex requirements
6. `ServeOrigin`, if present, starts with `http://` or `https://`
7. `ServePath` starts with `/`

Fail-fast: if any check fails, `New` returns an error and the service refuses to start.

## API surface

| Method | Purpose |
|---|---|
| `New(cfg Config) (*Kit, error)` | Construct and validate |
| `Kit.Client() inngestgo.Client` | Access native SDK client for `CreateFunction` |
| `Kit.Mount(mux *http.ServeMux)` | Register serve handler at ServePath |
| `Kit.Send(ctx, events...) ([]string, error)` | Emit events into inngest |

That's the entire API. Everything else (function creation, steps, retries, concurrency, cron triggers) lives in the native `inngestgo` SDK.

## For everything else

- [inngestgo SDK docs](https://pkg.go.dev/github.com/inngest/inngestgo)
- [inngest documentation](https://www.inngest.com/docs)
- [Self-hosting guide](https://www.inngest.com/docs/self-hosting)
