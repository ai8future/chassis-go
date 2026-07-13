# Lake response bound test timed out under hosted race instrumentation

The infinite-space test reader filled a 32 MiB decoder buffer one byte at a time. Under the GitHub-hosted race detector and concurrent package load, that test-only work exceeded its two-second deadline even though decoding remained byte-bounded.

The reader now fills exponentially with `copy`, and the assertion uses a scheduler-safe five-second deadline. Twenty repeated package race runs plus a two-processor full race run pass while retaining the same 32 MiB production bound.
