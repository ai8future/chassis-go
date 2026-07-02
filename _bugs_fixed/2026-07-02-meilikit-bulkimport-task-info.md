# meilikit BulkImport task visibility

`Index.BulkImport` previously discarded Meilisearch task responses, so callers could not observe asynchronous indexing outcomes for each submitted batch.

Fixed by returning one `TaskInfo` per batch while preserving progress callbacks and partial-task return on later errors.
