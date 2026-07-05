# Trace-ID Contract

Phase 0.5 minimal normative scaffold for flow tooling.

## Canonical format

`X-Trace-ID` is suite-wide canonical as:

```text
tr_[0-9a-f]{32}
```

That is 128 bits of lowercase hexadecimal entropy after the `tr_` prefix and maps
1:1 to a W3C/OTel trace id.

## Bounded legacy acceptance

During the documented migration window, inbound tooling may accept legacy
`tr_[0-9a-f]{12}` values only to preserve trace continuity. New generated IDs
must always use the 32-hex form. Arbitrary `tr_` strings are rejected/regenerated
rather than logged as-is.

## Flow author rule

Every Windmill flow step that calls a service must propagate `X-Trace-ID`. Use
`lib/wmglue.py` instead of hand-writing HTTP calls.
