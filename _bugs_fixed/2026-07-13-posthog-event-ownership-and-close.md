# PostHog queued events retained caller memory and Close raced flushes

Queued analytics events referenced caller-owned nested maps and slices until
deferred JSON serialization, allowing later mutation to alter payloads or race
the flush worker. `Close` could also return while an automatic flush remained
active and could overlap its final flush.

Events are now serialized into owned immutable bytes during enqueue. Flushes
are serialized, the worker is joined before the final flush, concurrent capture
is closed under the buffer lock, and repeated concurrent `Close` calls remain
idempotent without double sending.
