# Changelog

All notable changes to this project will be documented in this file.

## [1.0.4] - 2026-02-03

- Fix chassis.Version constant drift (was stuck at 1.0.0), add float64 to INTEGRATING.md type list (Claude:Opus 4.5)

## [1.0.3] - 2026-02-03

- Add float64 support to config.MustLoad (Claude:Opus 4.5)

## [1.0.2] - 2026-02-03

- Document chassis.Version in INTEGRATING.md (Claude:Opus 4.5)

## [1.0.1] - 2026-02-03

- Add exported `chassis.Version` constant for integrator diagnostics (Claude:Opus 4.5)

## [1.0.0] - 2026-02-03

- Initial project setup with VERSION, CHANGELOG, AGENTS.md, and standard directories
- Existing codebase includes: call (retry/breaker), config, grpckit, health, httpkit, lifecycle, logz, testkit
