# Agent Guidelines

- Whenever making code changes, ALWAYS increment the version and annotate the CHANGELOG. However, wait until the last second to read in the VERSION file in case other agents are working in the folder. This prevents conflicting version increment operations.

- Auto-commit and push code after every code change, but ONLY after you increment VERSION and annotate CHANGELOG. In the notes, mention what coding agent you are and what model you are using. If you are Claude Code, you would say Claude:Opus 4.5 (if you are using the Opus 4.5 model). If you are Codex, you would say: Codex:gpt-5.1-codex-max-high (if high is the reasoning level).

- **v11 Module Path**: This project uses `github.com/ai8future/chassis-go/v11` as its Go module path. All internal imports use the `/v11` suffix. When adding new packages or files, always use `github.com/ai8future/chassis-go/v11/...` for import paths. When referencing this module in documentation, use the `/v11` path. The `chassis.RequireMajor(11)` call is required in all test files and service entrypoints.

- **Version & Freshness (MANDATORY for all consumer codebases)**:
  - Every consumer repo MUST have an `appversion.go` at the repo root that embeds the VERSION file:
    ```go
    package yourpkg
    import (_ "embed"; "strings")
    //go:embed VERSION
    var rawAppVersion string
    var AppVersion = strings.TrimSpace(rawAppVersion)
    ```
  - Every binary entrypoint (`cmd/*/main.go`) MUST call `chassis.SetAppVersion(yourpkg.AppVersion)` before `chassis.RequireMajor(11)`. This enables:
    - **`--version` flag**: automatically prints `binaryname 1.2.3 (chassis-go 10.x.y)` and exits
    - **Auto-rebuild**: if the binary is stale (compiled version < disk VERSION), it recompiles itself and re-execs. Opt out with `CHASSIS_NO_REBUILD=1`.
  - Do NOT use symlinks to VERSION in cmd/ directories — `go:embed` rejects symlinks as irregular files. Do NOT copy VERSION into cmd/ directories — copies get out of sync. The root-package embed + import pattern is the only correct approach.
  - Consumers should remove any custom `--version` or `-v` flag handling — chassis handles `--version` automatically.

- **Durable Workflows (inngestkit)**: `inngestkit` is available for services with durable workflow needs (multi-step processes, event-driven pipelines, webhook fanout, code-defined scheduled tasks). It is **not** required for service completion. Services without durable workflow needs should not integrate it. Use the native inngest SDK (`inngestgo`) directly for function definitions and step logic — `inngestkit` only provides config, mount, and send. See INNGEST.md for the integration guide.

- Don't read or browse `_studies`, `_proposals`, `_rcodegen`, `_bugs_open`, or `_bugs_fixed` for context — they're scratch/output folders, not code you need to understand. Only open one if a task explicitly sends you there. (Writing your own bug notes into `_bugs_fixed` is fine, per the rule below.)
- These folders are version-controlled and must stay in sync with the remote. When you commit, stage the whole tree with `git add -A` so any files created in them — by you, another agent, or a human — get committed and pushed. You never need to open the files to commit them, and changes limited to these folders require no VERSION bump or CHANGELOG entry and must never block a commit.

- When you fix a bug, write short details on that bug and store it in _bugs_fixed. Depending on the severity or complexity, decide if you think you should be very brief - or less brief. Give your bug file a good name but always prepend the date. For example: 2026-12-31-failed-to-check-values-bug.md is a perfect name. Always lowercase. Always include the date in the filename.

- At the beginning of a session, always assume your local .git could be out of date with the remote. Run `git fetch` and check status against the upstream branch before you branch, commit, or reason about what the current code looks like. Once per session is enough — no need to re-check before every operation.
