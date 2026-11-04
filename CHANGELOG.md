# Changelog

All notable changes to this project are documented here.
Format: Keep a Changelog. Versioning: Semantic Versioning.
## [1.0.5] - 2026-11-04

### Fixed
- Update module github.com/jackc/pgx/v5 to v5.12.0 (#94) (deps)
- Update dependency gallery-dl to v1.32.19 (#95) (deps)
## [1.0.4] - 2026-11-01

### Fixed
- Update dependency gallery-dl to v1.32.18 (#86) (deps)

### CI
- Release only after check and Docker pass on the commit
## [1.0.3] - 2026-11-01

### Fixed
- Finish the SvelteKit 3 migration (frontend)
- Update module modernc.org/sqlite to v1.61.0 (deps)

### Build
- Let Renovate bump gallery-dl and ffmpeg in the image

### CI
- Cut a patch release when gallery-dl or ffmpeg is bumped
## [1.0.2] - 2026-10-19

### Changed
- Make golangci-lint pass

### Documentation
- Architecture overview, contributing guide, updated dev commands

### Build
- Adopt fleetlint with a task-runner contract, hooks and hardened CI
- Tidy go.sum and keep node_modules out of the Go module
- Add eslint, prettier and svelte-check; format and fix findings (frontend)
- Pin the Go toolchain to the patched 1.26.x release
## [1.0.0] - 2026-07-09

### Guard
- Accept origins from allowed hosts (host-rewriting proxies)
