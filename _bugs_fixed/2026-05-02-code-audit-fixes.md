# 2026-05-02 code audit fixes

Fixed audit findings that could disable documented freshness rebuilds, race the registry CLI poller, lose Kafka messages when DLQ publish failed, leave stale registry state after lifecycle startup failure, or panic when closing a subscriber before `Start`.

Also corrected stale v10 README import references and example entrypoints that did not set the app version before `RequireMajor(11)`.
