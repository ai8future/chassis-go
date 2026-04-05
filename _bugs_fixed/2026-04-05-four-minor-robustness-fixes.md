# 2026-04-05: Four minor robustness fixes

## otel/otel.go — shared shutdown timeout

The shutdown closure shared a single 5s context between `tp.Shutdown` and `mp.Shutdown`. If the trace provider consumed most of the timeout, the metric provider would get a nearly-expired context. Fixed by giving each its own `context.WithTimeout`.

## metrics/metrics.go — silently discarded errors

`Counter()` and `Histogram()` used `_, _ :=` when creating meters, silently swallowing creation errors. Changed to log warnings via `r.logger`.

## config/config.go — bare panic from MustCompile

The "pattern" validate case used `regexp.MustCompile(value)`, which panics with an opaque runtime error on bad patterns. Replaced with `regexp.Compile` + a descriptive `panic(fmt.Sprintf(...))`.

## health/handler.go — ignored Write error

`w.Write(buf.Bytes())` return value was discarded. Added error check with `slog.ErrorContext` logging.
