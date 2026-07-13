# Test version and webhook fuzz gates were incomplete

Two tracked test files lacked their required `RequireMajor(11)` call, and hosted CI did not enforce the contract per file. Webhook verification also lacked the bounded malformed-input fuzz smoke already used for encrypted envelopes.

The missing calls are restored, CI now scans every tracked non-scratch `*_test.go` file for qualified or unqualified `RequireMajor(11)`, and a bounded `VerifyPayloadID` fuzz target is run for ten seconds.
