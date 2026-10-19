# Contributing

Thanks for helping with Loupe. This page covers the mechanics; the design is in
[docs/ARCHITECTURE.md](docs/ARCHITECTURE.md) and the invariants in
[AGENTS.md](AGENTS.md).

## Setup

You need Go (the version in `go.mod`), Node (the version in `frontend/.nvmrc`)
and `make`. gallery-dl is only needed to run the app, not to test it.

```sh
make tools      # pinned dev tools: golangci-lint, govulncheck, gitleaks, fleetlint; npm ci
prek install    # git hooks (or: pre-commit install)
make run        # backend on :8787
cd frontend && npm run dev   # UI on :5173, proxies /api to :8787
```

Make sure `$(go env GOPATH)/bin` is on your `PATH` so the installed tools are found.

## The check targets

Every tool, hook and CI job goes through the same Makefile targets:

| target | when | what |
|---|---|---|
| `make fmt` | before committing | gofumpt + goimports, prettier |
| `make check-fast` | pre-commit | lint (golangci-lint, eslint, prettier, svelte-check) + the Go suite |
| `make check` | pre-push and CI | lint, race-tested suite, coverage floor, audit (govulncheck, npm audit, gitleaks, `go mod tidy -diff`), build, `fleetlint check` |

`make check` is the definition of green. If it passes locally it passes in CI.

Coverage is a ratchet: `COVER_MIN` in the Makefile may go up when you add
tests, never down. Generated sqlc code is excluded from the total.

## Commits

Commit messages follow [Conventional Commits](https://www.conventionalcommits.org/)
(`feat:`, `fix:`, `refactor:`, `perf:`, `test:`, `docs:`, `build:`, `ci:`,
`chore:`); the commit-msg hook enforces it and `git-cliff` builds the changelog
from them. Keep one logical change per commit.

## Pull requests

- Describe what changes and why; link the issue if there is one.
- `make check` must pass. New code comes with tests; new linter exemptions
  (`//nolint`, config exclusions) need a reason in the comment.
- Mutating API endpoints are POST-only behind the `guard` middleware; the UI is
  mobile-first (desktop styling lives in the `@media (min-width: 900px)` blocks).
- Schema changes are goose migrations, one per engine, plus `make sqlc`.

## Security

Please report vulnerabilities privately, as described in [SECURITY.md](SECURITY.md).
