# Subscriber concurrency only processed one record at a time

**Date**: 2026-03-30
**Severity**: High — concurrency feature was effectively non-functional

## Problem

The kafkakit subscriber's concurrency model (added in 10.2.0) used `PollFetches` + `wg.Wait()` per batch. Two compounding issues:

1. `PollFetches` returned whatever kgo had buffered (often just 1 record from a single partition), so the semaphore had nothing to parallelize.
2. `wg.Wait()` at the end of each batch blocked until all workers finished, then polled again — meaning even if multiple records arrived, the loop serialized batches.

## Fix

1. Switched from `PollFetches` to `PollRecords(ctx, maxPoll)` with `maxPoll` auto-scaled to `Concurrency * 2` so each poll fetches enough records to fill the worker pool.
2. Removed per-batch `wg.Wait()`. The semaphore now acts as a rolling concurrency limiter — the poll loop continues immediately. `WaitGroup` retained only for graceful shutdown drain via `defer wg.Wait()`.

Note: `kgo.FetchMaxRecords` does not exist in franz-go — the correct API is `PollRecords(ctx, maxRecords)` which limits records on the client side.
