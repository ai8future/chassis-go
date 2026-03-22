# XYOps Integration Guide (Go)

How to get the most out of xyops from a chassis-go service. This guide covers every integration pattern — from basic connectivity checks to full bidirectional job orchestration.

**If your service uses chassis, it should use xyops.** A chassis service without xyops is invisible to operations.

## Quick Start

```go
import (
    chassis "github.com/ai8future/chassis-go/v10"
    "github.com/ai8future/chassis-go/v10/config"
    "github.com/ai8future/chassis-go/v10/lifecycle"
    "github.com/ai8future/chassis-go/v10/logz"
    "github.com/ai8future/chassis-go/v10/xyops"
)

type Config struct {
    LogLevel string      `env:"LOG_LEVEL" default:"info"`
    Port     int         `env:"PORT" default:"8080"`
    Xyops    xyops.Config // XYOPS_BASE_URL, XYOPS_API_KEY, etc.
}

func main() {
    chassis.RequireMajor(10)
    cfg := config.MustLoad[Config]()
    log := logz.New(cfg.LogLevel)

    ops := xyops.New(cfg.Xyops,
        xyops.WithMonitoring(cfg.Xyops.MonitorInterval),
    )

    // ops.Run pushes metrics to xyops every N seconds
    lifecycle.Run(ctx, httpServer, ops.Run)
}
```

Set these env vars:

```
XYOPS_BASE_URL=https://xyops.example.com:5522
XYOPS_API_KEY=your-api-key
XYOPS_SERVICE_NAME=my-service
XYOPS_MONITOR_ENABLED=true
XYOPS_MONITOR_INTERVAL=30
```

That's it. Your service is now visible to operations.

---

## Part 1: Monitoring Bridge

The monitoring bridge is the single most important xyops integration. It makes your service visible — health, metrics, and alerting all flow through it.

### Basic monitoring (health only)

```go
ops := xyops.New(cfg.Xyops,
    xyops.WithMonitoring(30), // push every 30 seconds
)

lifecycle.Run(ctx, httpServer, ops.Run)
```

This registers your service with xyops and pushes a heartbeat every 30 seconds. If the heartbeat stops, xyops knows your service is down.

### Bridging application metrics

The real power is bridging your application's metrics into xyops so they can drive alerts.

```go
// Define gauges that implement xyops.MetricGauge
type gauge struct {
    value atomic.Int64
}
func (g *gauge) Value() float64 { return float64(g.value.Load()) }

var (
    requestLatencyP99 = &gauge{}
    queueDepth        = &gauge{}
    errorRate         = &gauge{}
    activeConnections = &gauge{}
    cacheHitRate      = &gauge{}
)

ops := xyops.New(cfg.Xyops,
    xyops.WithMonitoring(30),
    xyops.BridgeMetric("request_latency_p99", requestLatencyP99),
    xyops.BridgeMetric("queue_depth", queueDepth),
    xyops.BridgeMetric("error_rate", errorRate),
    xyops.BridgeMetric("active_connections", activeConnections),
    xyops.BridgeMetric("cache_hit_rate", cacheHitRate),
)
```

These become xyops custom monitors. Operations can write alert expressions against them:

- Alert when `request_latency_p99 > 500` for 5 minutes
- Alert when `queue_depth > 10000`
- Alert when `error_rate > 0.05`

### Wrapping existing OTel metrics as gauges

If you already have OTel metrics, wrap them:

```go
type otelGauge struct {
    observer metric.Float64Observable
    last     atomic.Int64 // store as int64 bits
}

func (g *otelGauge) Value() float64 {
    return math.Float64frombits(uint64(g.last.Load()))
}

// Update the gauge from your OTel callback
func (g *otelGauge) Set(v float64) {
    g.last.Store(int64(math.Float64bits(v)))
}
```

### Monitoring without lifecycle (testing / diagnostics)

```go
ops := xyops.New(cfg.Xyops) // no monitoring, just the client

// Verify xyops connectivity
if err := ops.Ping(ctx); err != nil {
    log.Error("xyops unreachable", "err", err)
}
```

---

## Part 2: Event & Job Management

### Triggering events

Events are the core xyops primitive. An event defines what to do; triggering it creates a job.

```go
// Simple event trigger
jobID, err := ops.RunEvent(ctx, "deploy-prod", map[string]string{
    "version":     "2.4.1",
    "environment": "production",
})
if err != nil {
    return fmt.Errorf("failed to trigger deploy: %w", err)
}
log.Info("deploy started", "job_id", jobID)
```

### Polling job status

```go
// Poll until completion
for {
    status, err := ops.GetJobStatus(ctx, jobID)
    if err != nil {
        return err
    }

    log.Info("job progress",
        "job_id", jobID,
        "state", status.State,
        "progress", status.Progress,
    )

    switch status.State {
    case "completed":
        log.Info("job finished", "output", status.Output, "exit_code", status.ExitCode)
        return nil
    case "failed":
        return fmt.Errorf("job failed: exit %d: %s", status.ExitCode, status.Output)
    }

    time.Sleep(5 * time.Second)
}
```

`GetJobStatus` results are cached (5 min TTL, 500 entries) so polling is cheap.

### Cancelling jobs

```go
if err := ops.CancelJob(ctx, jobID); err != nil {
    log.Error("failed to cancel job", "job_id", jobID, "err", err)
}
```

Cache is automatically invalidated on cancel.

### Searching job history

```go
// Find recent deploy jobs
jobs, err := ops.SearchJobs(ctx, "deploy-prod")
if err != nil {
    return err
}
for _, job := range jobs {
    fmt.Printf("[%s] %s — %s (exit %d)\n", job.ID, job.State, job.Output, job.ExitCode)
}
```

### Listing available events

```go
events, err := ops.ListEvents(ctx)
if err != nil {
    return err
}
for _, ev := range events {
    fmt.Printf("  %s: %s\n", ev.ID, ev.Name)
}
```

### Getting a specific event

```go
event, err := ops.GetEvent(ctx, "deploy-prod")
if err != nil {
    return err
}
log.Info("event details", "id", event.ID, "name", event.Name)
```

---

## Part 3: Alert Management

### Listing active alerts

```go
alerts, err := ops.ListActiveAlerts(ctx)
if err != nil {
    return err
}
for _, alert := range alerts {
    log.Warn("active alert", "id", alert.ID, "message", alert.Message, "state", alert.State)
}
```

### Acknowledging alerts

```go
for _, alert := range alerts {
    if err := ops.AckAlert(ctx, alert.ID); err != nil {
        log.Error("failed to ack alert", "alert_id", alert.ID, "err", err)
    }
}
```

### Alert-driven automation

A powerful pattern: your service reacts to its own alerts.

```go
// Periodic alert check as a lifecycle component
func alertWatcher(ops *xyops.Client, log *slog.Logger) func(context.Context) error {
    return func(ctx context.Context) error {
        return tick.Every(60*time.Second, func(ctx context.Context) error {
            alerts, err := ops.ListActiveAlerts(ctx)
            if err != nil {
                return err
            }
            for _, alert := range alerts {
                switch {
                case strings.Contains(alert.Message, "queue_depth"):
                    log.Warn("auto-scaling workers due to queue depth alert")
                    scaleWorkers(ctx, 10)
                    ops.AckAlert(ctx, alert.ID)
                case strings.Contains(alert.Message, "disk_usage"):
                    log.Warn("triggering cleanup due to disk alert")
                    ops.RunEvent(ctx, "cleanup-temp", nil)
                    ops.AckAlert(ctx, alert.ID)
                }
            }
            return nil
        })(ctx)
    }
}

lifecycle.Run(ctx, httpServer, ops.Run, alertWatcher(ops, log))
```

---

## Part 4: Webhooks

### Firing webhooks through xyops

```go
deliveryID, err := ops.FireWebhook(ctx, "deploy-hook", map[string]any{
    "service": "my-service",
    "version": "2.4.1",
    "status":  "deployed",
})
if err != nil {
    log.Error("webhook failed", "err", err)
}
log.Info("webhook sent", "delivery_id", deliveryID)
```

Under the hood, `FireWebhook` delegates to chassis `webhook.Sender`:
- HMAC-SHA256 signature via `seal.Sign`
- Automatic retry with exponential backoff on 5xx
- Delivery tracking — query status later if needed

---

## Part 5: Raw API Escape Hatch

For any xyops API endpoint not covered by the curated methods:

```go
// GET with no body
resp, err := ops.Raw(ctx, "GET", "/api/servers", nil)

// POST with body
resp, err := ops.Raw(ctx, "POST", "/api/custom/action", map[string]any{
    "target": "db-primary",
    "action": "failover",
})

// Parse the response
var result MyCustomType
json.Unmarshal(resp, &result)
```

`Raw` goes through the same `call` pipeline — retry, circuit breaking, auth headers, OTel tracing.

---

## Part 6: Worker — Executing Jobs FROM xyops

If your service executes jobs dispatched by xyops (deployments, migrations, batch processing), add the worker module. This makes your service act as an xyops satellite.

### Basic worker setup

```go
import "github.com/ai8future/chassis-go/v10/xyopsworker"

type Config struct {
    LogLevel string              `env:"LOG_LEVEL" default:"info"`
    Worker   xyopsworker.Config  // XYOPS_WORKER_MASTER_URL, XYOPS_WORKER_SECRET_KEY, etc.
}

func main() {
    chassis.RequireMajor(10)
    cfg := config.MustLoad[Config]()

    worker := xyopsworker.New(cfg.Worker)

    worker.Handle("deploy", deployHandler)
    worker.Handle("db-migrate", migrateHandler)
    worker.Handle("cleanup", cleanupHandler)

    lifecycle.Run(ctx, worker.Run)
}
```

Set these env vars:

```
XYOPS_WORKER_MASTER_URL=wss://xyops.example.com:5523
XYOPS_WORKER_SECRET_KEY=your-satellite-key
XYOPS_WORKER_HOSTNAME=worker-01
XYOPS_WORKER_GROUPS=deploy,batch
```

### Writing handlers

Handlers receive a `Job` with rich reporting capabilities:

```go
func deployHandler(ctx context.Context, job xyopsworker.Job) error {
    env := job.Params["environment"]
    version := job.Params["version"]

    job.Log("Starting deployment")
    job.Progress(0, "Initializing")

    // Step 1: Pull image
    job.Progress(20, "Pulling image "+version)
    job.Log("Pulling container image for " + version)
    if err := pullImage(ctx, version); err != nil {
        return fmt.Errorf("image pull failed: %w", err)
    }

    // Step 2: Run migrations
    job.Progress(40, "Running database migrations")
    job.Log("Applying migrations for " + env)
    if err := runMigrations(ctx, env); err != nil {
        return fmt.Errorf("migration failed: %w", err)
    }

    // Step 3: Deploy
    job.Progress(60, "Deploying to " + env)
    job.Log("Rolling out new version")
    if err := rollout(ctx, env, version); err != nil {
        return fmt.Errorf("rollout failed: %w", err)
    }

    // Step 4: Verify
    job.Progress(80, "Running smoke tests")
    job.Log("Verifying deployment health")
    if err := smokeTest(ctx, env); err != nil {
        // Rollback on failure
        job.Log("Smoke test failed, rolling back")
        rollback(ctx, env)
        return fmt.Errorf("smoke test failed: %w", err)
    }

    job.Progress(100, "Complete")
    job.SetOutput(fmt.Sprintf("Deployed %s to %s successfully", version, env))
    return nil
}
```

### Multi-step batch processing

```go
func batchImportHandler(ctx context.Context, job xyopsworker.Job) error {
    source := job.Params["source"]
    job.Log("Loading records from " + source)

    records, err := loadRecords(ctx, source)
    if err != nil {
        return err
    }

    total := len(records)
    var processed, failed int

    for i, record := range records {
        // Check for cancellation (context is cancelled on abort/timeout)
        if ctx.Err() != nil {
            job.Log(fmt.Sprintf("Cancelled after %d/%d records", processed, total))
            return ctx.Err()
        }

        if err := processRecord(ctx, record); err != nil {
            failed++
            job.Log(fmt.Sprintf("Record %d failed: %v", i, err))
        } else {
            processed++
        }

        // Report progress every 100 records
        if i%100 == 0 {
            pct := (i * 100) / total
            job.Progress(pct, fmt.Sprintf("%d/%d processed, %d failed", processed, total, failed))
        }
    }

    job.SetOutput(fmt.Sprintf("Imported %d records, %d failed out of %d total", processed, failed, total))
    return nil
}
```

### Database migration handler

```go
func migrateHandler(ctx context.Context, job xyopsworker.Job) error {
    database := job.Params["database"]
    direction := job.Params["direction"] // "up" or "down"

    job.Log(fmt.Sprintf("Starting %s migration for %s", direction, database))
    job.Progress(10, "Connecting to database")

    db, err := connectDB(ctx, database)
    if err != nil {
        return fmt.Errorf("connection failed: %w", err)
    }
    defer db.Close()

    job.Progress(30, "Reading pending migrations")
    pending, err := getPendingMigrations(db, direction)
    if err != nil {
        return err
    }

    job.Log(fmt.Sprintf("Found %d pending migrations", len(pending)))

    for i, m := range pending {
        pct := 30 + ((i + 1) * 60 / len(pending))
        job.Progress(pct, fmt.Sprintf("Applying %s", m.Name))
        job.Log(fmt.Sprintf("Applying migration: %s", m.Name))

        if err := m.Apply(ctx, db); err != nil {
            return fmt.Errorf("migration %s failed: %w", m.Name, err)
        }
    }

    job.Progress(100, "Complete")
    job.SetOutput(fmt.Sprintf("Applied %d migrations to %s", len(pending), database))
    return nil
}
```

### Testing handlers locally

Use `Dispatch` to test without a WebSocket connection:

```go
func TestDeployHandler(t *testing.T) {
    chassis.RequireMajor(10)

    worker := xyopsworker.New(xyopsworker.Config{
        MasterURL: "wss://unused",
        SecretKey: "unused",
    })
    worker.Handle("deploy", deployHandler)

    job := xyopsworker.Job{
        ID:      "test-1",
        EventID: "deploy",
        Params:  map[string]string{"environment": "staging", "version": "1.0.0"},
    }

    err := worker.Dispatch(context.Background(), job)
    if err != nil {
        t.Fatalf("deploy handler failed: %v", err)
    }
}
```

---

## Part 7: Service That Does Both (Client + Worker)

Many services both call xyops AND execute jobs from it. Here's the full pattern:

```go
type Config struct {
    LogLevel string              `env:"LOG_LEVEL" default:"info"`
    Port     int                 `env:"PORT" default:"8080"`
    Xyops    xyops.Config
    Worker   xyopsworker.Config
}

func main() {
    chassis.RequireMajor(10)

    d := deploy.Discover("my-service")
    d.LoadEnv() // loads XYOPS_API_KEY, XYOPS_WORKER_SECRET_KEY from deploy dir

    cfg := config.MustLoad[Config]()
    log := logz.New(cfg.LogLevel)

    // Client: call xyops API + monitoring bridge
    ops := xyops.New(cfg.Xyops,
        xyops.WithMonitoring(30),
        xyops.BridgeMetric("queue_depth", queueGauge),
        xyops.BridgeMetric("active_jobs", activeJobsGauge),
    )

    // Worker: execute jobs from xyops
    worker := xyopsworker.New(cfg.Worker)
    worker.Handle("deploy", deployHandler)
    worker.Handle("rollback", rollbackHandler)
    worker.Handle("db-migrate", migrateHandler)

    // HTTP handlers can trigger events through the client
    mux := http.NewServeMux()
    mux.HandleFunc("POST /api/deploy", func(w http.ResponseWriter, r *http.Request) {
        jobID, err := ops.RunEvent(r.Context(), "deploy-prod", map[string]string{
            "version": r.URL.Query().Get("v"),
        })
        if err != nil {
            httpkit.JSONError(w, r, 500, err.Error())
            return
        }
        json.NewEncoder(w).Encode(map[string]string{"job_id": jobID})
    })

    mux.HandleFunc("GET /api/alerts", func(w http.ResponseWriter, r *http.Request) {
        alerts, err := ops.ListActiveAlerts(r.Context())
        if err != nil {
            httpkit.JSONError(w, r, 500, err.Error())
            return
        }
        json.NewEncoder(w).Encode(alerts)
    })

    handler := httpkit.Recovery(log)(httpkit.RequestID(httpkit.Logging(log)(mux)))
    srv := &http.Server{Addr: fmt.Sprintf(":%d", cfg.Port), Handler: handler}

    lifecycle.Run(ctx,
        func(ctx context.Context) error {
            errCh := make(chan error, 1)
            go func() { errCh <- srv.ListenAndServe() }()
            select {
            case <-ctx.Done():
                return srv.Shutdown(context.Background())
            case err := <-errCh:
                return err
            }
        },
        ops.Run,     // monitoring bridge
        worker.Run,  // job execution
    )
}
```

---

## Part 8: CLI Tools

CLI tools and batch processes should use xyops for job orchestration and operational visibility.

### CLI tool that triggers and waits for a job

```go
func main() {
    chassis.RequireMajor(10)
    cfg := config.MustLoad[CLIConfig]()

    registry.InitCLI(chassis.Version)
    defer registry.ShutdownCLI(0)

    ops := xyops.New(cfg.Xyops)

    // Verify connectivity
    if err := ops.Ping(ctx); err != nil {
        log.Fatal("xyops unreachable", "err", err)
    }

    // Trigger event and wait
    jobID, err := ops.RunEvent(ctx, "nightly-etl", map[string]string{
        "date": time.Now().Format("2006-01-02"),
    })
    if err != nil {
        log.Fatal("failed to trigger job", "err", err)
    }

    registry.Status("waiting for job " + jobID)

    for {
        status, _ := ops.GetJobStatus(ctx, jobID)
        registry.Progress(status.Progress, 100, 0)

        if status.State == "completed" {
            fmt.Println("Job completed:", status.Output)
            return
        }
        if status.State == "failed" {
            fmt.Fprintf(os.Stderr, "Job failed (exit %d): %s\n", status.ExitCode, status.Output)
            registry.ShutdownCLI(1)
            os.Exit(1)
        }

        time.Sleep(5 * time.Second)
    }
}
```

### CLI tool that orchestrates multiple jobs

```go
func main() {
    chassis.RequireMajor(10)
    cfg := config.MustLoad[CLIConfig]()

    registry.InitCLI(chassis.Version)
    defer registry.ShutdownCLI(0)

    ops := xyops.New(cfg.Xyops)

    environments := []string{"staging", "canary", "production"}

    for i, env := range environments {
        registry.Status(fmt.Sprintf("deploying to %s (%d/%d)", env, i+1, len(environments)))

        jobID, err := ops.RunEvent(ctx, "deploy", map[string]string{
            "environment": env,
            "version":     cfg.Version,
        })
        if err != nil {
            log.Fatal("deploy trigger failed", "env", env, "err", err)
        }

        // Wait for this stage to complete before moving to next
        if err := waitForJob(ctx, ops, jobID); err != nil {
            log.Fatal("deploy failed", "env", env, "err", err)
        }

        registry.Progress(i+1, len(environments), 0)
        log.Info("stage complete", "env", env)
    }

    registry.Status("all deployments complete")
}

func waitForJob(ctx context.Context, ops *xyops.Client, jobID string) error {
    for {
        status, err := ops.GetJobStatus(ctx, jobID)
        if err != nil {
            return err
        }
        switch status.State {
        case "completed":
            return nil
        case "failed":
            return fmt.Errorf("exit %d: %s", status.ExitCode, status.Output)
        }
        time.Sleep(3 * time.Second)
    }
}
```

### CLI tool that checks alerts

```go
func main() {
    chassis.RequireMajor(10)
    cfg := config.MustLoad[CLIConfig]()

    ops := xyops.New(cfg.Xyops)

    alerts, err := ops.ListActiveAlerts(ctx)
    if err != nil {
        log.Fatal("failed to list alerts", "err", err)
    }

    if len(alerts) == 0 {
        fmt.Println("No active alerts.")
        return
    }

    fmt.Printf("%d active alert(s):\n\n", len(alerts))
    for _, a := range alerts {
        fmt.Printf("  [%s] %s (%s)\n", a.ID, a.Message, a.State)
    }

    // Auto-ack if --ack flag
    if cfg.AutoAck {
        for _, a := range alerts {
            ops.AckAlert(ctx, a.ID)
            fmt.Printf("  Acknowledged: %s\n", a.ID)
        }
    }
}
```

---

## Part 9: Deploy Directory Integration

Use `deploy` to load xyops credentials from the filesystem instead of hardcoding them:

```go
d := deploy.Discover("my-service")
d.LoadEnv() // loads ~/deploy/my-service/secrets.env into process env
// Now XYOPS_API_KEY and XYOPS_WORKER_SECRET_KEY are available
cfg := config.MustLoad[Config]()
```

The `secrets.env` file contains:
```
XYOPS_API_KEY=abc123...
XYOPS_WORKER_SECRET_KEY=xyz789...
```

This keeps secrets out of container images and CI/CD pipelines.

---

## Summary

| Pattern | Module | Use Case |
|---------|--------|----------|
| Health monitoring | `xyops` client + `WithMonitoring` | Every service |
| Application metrics | `xyops` client + `BridgeMetric` | Services with key metrics |
| Job triggering | `xyops` client `RunEvent` | Services that start work |
| Job status polling | `xyops` client `GetJobStatus` | Services/CLIs that wait for results |
| Alert management | `xyops` client `ListActiveAlerts` / `AckAlert` | Alert-aware services |
| Alert-driven automation | `xyops` client + `tick` | Self-healing services |
| Webhook dispatch | `xyops` client `FireWebhook` | Event-driven integrations |
| Job execution | `xyopsworker` + `Handle` | Services that do work for xyops |
| Multi-step jobs | `xyopsworker` `Progress` / `Log` / `SetOutput` | Complex job handlers |
| CLI orchestration | `xyops` client + `registry.InitCLI` | Deployment scripts, batch tools |
| Secrets management | `deploy.Discover` + `LoadEnv` | All environments |
| Escape hatch | `xyops` client `Raw` | Custom xyops API endpoints |

**The minimum viable integration is: create the client, enable monitoring, add `ops.Run` to lifecycle.** Everything else is additive.
