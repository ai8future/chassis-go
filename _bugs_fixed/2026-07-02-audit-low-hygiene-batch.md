# Audit low-hygiene batch

Fixed the remaining low-risk audit hygiene items after the medium remediation batch.

Highlights:
- Phasekit tests now restore hydrated environment variables and tolerate slower race/full-suite subprocess startup.
- Call client spans now use method-only names and retry drains are capped at 1 MiB.
- Flagz rejects nil sources in Multi at construction time.
- Errors WriteProblem logging no longer panics for nil requests on encode failure.
- Tick jitter timers are stopped on cancellation.
- Guard HeaderKey docs now warn that client-controlled headers can bypass rate limits.
- Deploy hook errors include a bounded tail of hook output.
- Inferkit closes success response bodies via deferred close around JSON decode.
- golang.org/x/crypto and related x/* modules were updated to current compatible versions.
