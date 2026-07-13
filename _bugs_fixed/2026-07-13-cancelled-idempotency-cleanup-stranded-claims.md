# Cancelled idempotency cleanup stranded claims

Panic, response-limit, and handler-5xx cleanup inherited the request context. If that context was already cancelled, a store could reject the release and leave the key in flight until expiry.

Claim release now uses `context.WithoutCancel` to preserve request values while detaching cancellation, plus a two-second timeout to bound store cleanup. Regression tests cover cancelled panic, 5xx, and oversized-response paths and confirm the key can be claimed again. Ambiguous completion remains intentionally retained.
