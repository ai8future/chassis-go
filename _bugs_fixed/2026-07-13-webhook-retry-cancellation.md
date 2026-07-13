# Webhook retry and cancellation defects

Non-positive attempt counts could bypass delivery and panic while recording the failure, retries ignored caller cancellation, and responses were closed without bounded draining. Attempts now default to three, `SendContext` cancels requests and backoff, `Send` remains compatible, and response draining is bounded.
