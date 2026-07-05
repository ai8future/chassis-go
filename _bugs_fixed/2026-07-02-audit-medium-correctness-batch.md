# Audit medium correctness/security batch

Fixed the remaining medium-risk audit findings across freshness, registry, work, config, schemakit, kafkakit, heartbeatkit, and deploy.

Highlights:
- Freshness rebuilds now use unique temp paths beside the binary and chmod before rename.
- Registry rejects unsafe base paths and atomically claims command files.
- Work primitives recover task panics as structured task errors.
- Config validation splitting preserves regex quantifier commas.
- Schemakit rejects SchemaID 0 on serialize/deserialize.
- Kafkakit applies producer retries/backoff and deterministic most-specific handler selection.
- Heartbeat publishing is bounded by per-publish contexts.
- Deploy env parsing supports long lines up to 1 MiB and reports scanner failures.
