# CI and Docker resilience false-green fixes

Scheduled CI could lose nightly diagnostics or hide a failed `tee`, required Docker E2E could pass by skipping unavailable Docker, the Redpanda restart probe checked only broker health, and owned-resource cleanup failures were discarded while emitting unconditional completion markers.

The regressions now execute missing-directory and producer/`tee` failures, optional and required Docker availability, chassis Kafka publish/consume before and after a real broker restart, and cleanup failures that preserve primary failures while making otherwise successful owners fail with truthful markers.
