# Nightly fuzz discovery false green

Root cause: `scripts/test-nightly.sh` discovered fuzz targets with a process-substitution pipeline that redirected `go test -list` stderr to `/dev/null`. If one package failed discovery while another package listed fuzz targets, the nightly script could omit the failed package and still report success.

Fix: fuzz discovery now captures each package's target list explicitly, leaves discovery stderr visible, and returns failure immediately when any package's `go test -list '^Fuzz'` command fails.

Regression: `internal/citopology` now asserts the script no longer suppresses discovery stderr and executes a fake-`go` negative proof where one package has a fuzz target and a second package fails discovery; the nightly script must fail and include the discovery error.
