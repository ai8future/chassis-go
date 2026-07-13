# Kafka rebalance, tenant, and session validation gaps

Subscriber validation ran before `WithTenant`, so a valid option-provided tenant could not enable filtering. Positive session timeouts below franz-go's 100 ms floor were also accepted until client construction. Manual consumers did not react when franz-go reported that rebalance callbacks were blocked by an owned poll batch.

Validation now uses the effective subscriber tenant and rejects 1-99 ms session timeouts. Manual consumption wires `OnPartitionsCallbackBlocked` to cancel and boundedly drain the batch, commit only durable contiguous prefixes, allow the rebalance, and resume polling; interrupted handlers are not sent to the DLQ.
