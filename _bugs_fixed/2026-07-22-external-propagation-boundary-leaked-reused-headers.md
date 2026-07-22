# External propagation boundary leaked reused headers

An explicit TraceContext-only `call.Client` previously controlled new
injection but did not remove Baggage or custom propagation fields already
present on a reused request. The request header map was also shared with the
caller, so an earlier default client could contaminate the later external
request.

The boundary now clones headers, removes fields declared by the active global
and selected propagators, and injects only the selected propagator. Explicit
nil performs the scrub without injection. Regression tests use hostile global
propagation, typed and untyped nil, and request reuse across two clients under
the race detector.
