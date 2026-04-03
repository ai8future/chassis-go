# Kafka offsets committed before processing complete

**Date:** 2026-03-30

**Problem:** kafkakit subscriber auto-commits offsets when `PollFetches`/`PollRecords` is called, meaning offsets advance before handlers finish processing. If the service restarts mid-processing, those messages are lost — Kafka thinks they're done but the work was never completed. This is at-most-once delivery when at-least-once is needed for slow handlers.

**Fix:** Added `AtLeastOnce bool` to `SubscriberConfig`. When enabled:
- `kgo.DisableAutoCommit()` stops automatic offset advancement
- `CommitUncommittedOffsets` called after all handlers in a batch complete
- `OnPartitionsRevoked` callback drains workers and commits before rebalance
- Unconditional shutdown commit added for both modes

**Trade-off:** At-least-once means messages may be reprocessed after a crash. Consumers must handle duplicates (equinox_graph already does via entity/edge dedup).
