# Stale idempotency claims could mutate replacement ownership

The key-only memory store could not distinguish a stale request from a newer
claim for the same tenant and key. Late completion or release could overwrite
or delete replacement ownership, and in-flight records did not expire safely.

`MemoryStore` now implements an additive token-aware `LeaseStore`. Opaque random
tokens gate completion and release, expired records are removed before bounded
capacity checks, and stale owners receive `ErrLeaseLost` without mutation.
