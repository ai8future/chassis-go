# Registry mutations retried without idempotency guarantees

`registrykit.WithRetry` enabled the generic method-agnostic retry behavior, so
transient failures could cause registry POST mutations to be sent more than
once. Registry retries now apply the existing idempotent-only policy: reads can
retry while mutation POSTs receive one attempt. The generic `call.WithRetry`
v11 behavior remains compatible and is explicitly documented as requiring
duplicate-safe operations or `WithIdempotentOnlyRetries`.
