# Kafka poison records were treated as successful

Malformed envelopes and records without handlers were previously logged and considered handled. They now enter a bounded metadata-preserving DLQ path along with handler errors and panics; a failed DLQ publication remains non-durable and blocks later commits.
