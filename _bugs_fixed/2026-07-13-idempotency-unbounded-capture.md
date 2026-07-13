# Idempotency middleware captured bodies without bounds

Keyed request and response bodies were buffered without configurable limits,
allowing high memory use. A nil tenant resolver also contradicted its documented
single-tenant behavior by panicking when invoked.

Request and response capture now use conservative bounded defaults with safe
non-positive normalization. Oversized requests return 413 before the handler;
oversized responses return 500 without persistence, and nil tenant resolvers
restore the default single-tenant namespace.
