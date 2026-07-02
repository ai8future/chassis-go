# Critical HTTP client edge-case fixes

Fixed four edge-case bugs from the audit remediation plan:

- `posthogkit.Capture` could panic after `Close` because it sent on a closed flush channel.
- `call.WithRetry` could resend an empty or consumed request body when retries needed a body that could not be rewound.
- `lakekit` decoded successful responses without a size bound, allowing an untrusted server to stream indefinitely.
- `ollamakit.ChatStream` and `PullModel` inherited the regular client timeout, aborting valid long-running streams or pulls despite an active caller context.

Regression tests cover each fix. Verified with targeted race tests and `go build ./...`.
