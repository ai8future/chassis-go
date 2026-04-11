# inngestkit: signing key usability trap, weak key acceptance, missing validations

**Date:** 2026-04-11
**Severity:** Medium
**Package:** inngestkit

## Issues

1. **Signing key prefix not stripped** - The inngest ecosystem uses keys in `signkey-<env>-<hex>` format, but inngestkit expected raw hex only. Users pasting full keys from inngest server config got a confusing "must be valid hex" error.

2. **No minimum signing key length** - A 2-char hex key (`"aa"`) passed validation. HMAC-SHA256 with a 1-byte key is trivially brute-forceable.

3. **ServeOrigin not validated** - A malformed URL (e.g. `ftp://bad`) was silently accepted, only failing at runtime when inngest tried to call back.

4. **Redundant RegisterURL** - Hardcoded `baseURL + "/fn/register"` duplicated what the SDK already computes from `APIBaseURL`.

## Fixes

- Auto-strip `signkey-\w+-` prefix before validation using the same regex the inngestgo SDK uses
- Enforce minimum 32 hex chars (16 bytes) for signing keys
- Validate ServeOrigin starts with `http://` or `https://`
- Removed redundant RegisterURL override
