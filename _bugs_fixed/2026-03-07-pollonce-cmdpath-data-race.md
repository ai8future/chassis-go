# Data race: pollOnce reads cmdPath without mutex

**Date**: 2026-03-07

`pollOnce()` read the module-level `cmdPath` string without holding the mutex, while `Init()` writes it under the mutex. Under `-race -count=3`, this consistently triggered a data race between `TestPollOnceStopCommand`'s leaked goroutine and the next test's `Init()` call.

**Fix**: Read `cmdPath` under the mutex at the start of `pollOnce()`, then use the local copy for `os.ReadFile` and `os.Remove`.
