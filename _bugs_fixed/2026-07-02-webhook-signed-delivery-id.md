# webhook signed delivery ID

Webhook signatures previously covered only `timestamp.body`, so the delivery ID used for replay deduplication was not authenticated. A replay could swap `X-Webhook-Id` while preserving a valid body signature.

Fixed by signing `timestamp.id.body` and adding `VerifyPayloadID` to return the authenticated delivery ID to receivers. This is a wire-format change; senders and receivers must upgrade together.
