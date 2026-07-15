# G009 stale volume-parity evidence

The G009 changelog incorrectly claimed the selected pinned-Redpanda test restores the daemon's total Docker volume inventory to its preflight state. The concurrency-safe test instead proves that every exact captured Redpanda container-owned volume ID disappears and intentionally permits unrelated daemon volume churn.

Corrected the release evidence wording only; implementation and tests are unchanged.
