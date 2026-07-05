# Fixture Equality Rules

These fixtures are semantic contract examples, not byte-for-byte wire captures.

- JSON objects compare after canonicalization with sorted keys and insignificant whitespace removed.
- HTTP header names compare case-insensitively.
- Fields documented as sets (for example `capabilities` and `entity_refs`) compare order-insensitively.
- Placeholder tokens such as `{{trace_id}}`, `{{event_id}}`, and `{{now}}` match values that satisfy the corresponding contract pattern.
- Raw byte equality is required only where a fixture explicitly says so, such as an idempotency replay body.
