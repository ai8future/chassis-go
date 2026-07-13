# Idempotency ambiguous completion exposed false success

Keyed HTTP responses were written to clients before `Store.Complete` succeeded,
and completion errors were ignored. A client could therefore observe success
without a durable replay record and repeat a business mutation.

The middleware now buffers keyed responses until persistence succeeds. An
ambiguous completion fails closed with HTTP 503 plus `Retry-After: 1`, never
releases the claim, and never exposes the handler success. The legacy `Store`
contract remains source-compatible; claim retention after ambiguity remains a
store responsibility.
