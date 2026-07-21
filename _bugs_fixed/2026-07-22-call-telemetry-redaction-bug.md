# Call telemetry redaction

The `call` client recorded request paths and hosts on spans, hosts on duration
metrics, and raw token or transport errors in span events and status text. A
`net/http` transport failure can wrap the complete request URL in `url.Error`,
so scanner credentials carried in a query could reach an exporter even when a
consumer sanitized the error it returned to its own callers. Custom retry
reasons were another caller-controlled telemetry field.

`call.WithTelemetryRedaction` now provides an explicit compatibility-safe
mode. It keeps client spans, TraceContext injection, token injection, timeout,
retry, and circuit-breaker behavior, while limiting telemetry to canonical
HTTP methods, numeric status codes, fixed retry metadata, and fixed error
classifications. Destination and raw error data are omitted from both spans
and duration metrics. Redacted mode also initializes a nil request-header map
before TraceContext or token injection; existing clients retain their prior
telemetry unless they opt in.

In-memory trace and metric exporters verify successful retries, propagation,
timeouts, body-rewind errors, redirects, breaker outcomes, transport
`url.Error` chains, token and breaker failures, HTTP errors, unknown methods,
and unchanged default telemetry. The module also raises `golang.org/x/text` to
the GO-2026-5970 patched floor.
