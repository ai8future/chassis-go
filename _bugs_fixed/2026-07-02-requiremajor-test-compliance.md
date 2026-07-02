# RequireMajor test compliance and registry import cycle

Fixed the remaining test-harness compliance gap: every test file now calls `chassis.RequireMajor(11)`.

To let internal `registry` package tests call `chassis.RequireMajor(11)` without importing a package that imports `registry` back, app-version state now lives in a small internal shared package. `chassis.SetAppVersion` and `registry.SetAppVersion` continue to set the same process-wide app version.
