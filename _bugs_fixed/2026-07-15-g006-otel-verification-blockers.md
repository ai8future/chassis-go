# G006 OTel verification blockers

The pinned OpenTelemetry Collector runs as UID/GID 10001, but the live test and nightly restart probe bind-mounted host-owned receipt directories that were not writable by that user on Linux. The live test also kept trace and metric receipts only in `t.TempDir`, so the nightly artifact upload could not retain them. Separately, the directly imported franz-go `kmsg` module was classified as indirect.

The fix gives only dedicated receipt directories temporary cross-user write access, restores host-only permissions during cleanup, and uses unique nightly artifact subdirectories for every OTel integration repetition. Nightly now fails if trace, metric, or summary receipts are empty. The module declaration now classifies `kmsg` as direct.

Focused topology assertions and the real pinned collector integration cover the repaired behavior without adding production seams or dependencies.
