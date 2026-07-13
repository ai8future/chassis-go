# Concurrent Kafka handling could commit past failed offsets

The prior at-least-once batch model processed records concurrently within a partition and committed the entire batch, which could skip a failed earlier offset. Manual-contiguous mode now processes each partition serially, commits only its durable prefix with franz-go next-offset semantics, and stops on non-durable DLQ or commit failures.
