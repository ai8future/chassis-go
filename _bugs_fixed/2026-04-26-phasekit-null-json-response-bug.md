# Phasekit null JSON response bug

Phasekit accepted a top-level `null` response from the Phase CLI as an empty
secret set because unmarshalling JSON `null` into a Go map returns a nil map
without error. This could let a malformed CLI/server response look like a valid
empty export.

Fixed by rejecting nil parsed secret maps with `phase JSON must be an object`.
Added regression coverage for top-level `null` and array JSON responses.
