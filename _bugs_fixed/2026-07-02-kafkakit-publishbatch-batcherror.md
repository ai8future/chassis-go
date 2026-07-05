# kafkakit PublishBatch per-record failure reporting

`Publisher.PublishBatch` previously returned only the first produce error, leaving callers unable to distinguish records that were durably produced from records that failed. A whole-batch retry could duplicate successful records.

Fixed by returning `*BatchError` with per-record `BatchFailure` entries and a `Succeeded` count when one or more records fail.
