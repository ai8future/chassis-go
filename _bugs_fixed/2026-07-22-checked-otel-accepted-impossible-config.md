# Checked OpenTelemetry accepted impossible configuration

`otel.InitChecked` relied on lazy gRPC exporter constructors, so malformed
host/port values and an effective TLS minimum above the caller's maximum could
return success and install global providers. Concurrent or repeated
initialization could also mix global ownership, and shutdown left globals
pointing at drained providers.

Checked initialization now validates local endpoint and TLS policy before
construction, admits one active installation, and returns an idempotent
shutdown that relinquishes globals to explicit no-op providers before draining
the owned pipelines. Collector reachability and TLS handshake are intentionally
lazy and require export/receipt evidence. Regression coverage includes both
trace and metric TLS export, hostile plaintext environment, invalid policy,
concurrency, repeated initialization, and global reset.
