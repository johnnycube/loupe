# Changelog

All notable changes to this project are documented here.
Format: Keep a Changelog. Versioning: Semantic Versioning.
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
