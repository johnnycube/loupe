# Loupe — agent notes

Self-hosted gallery watcher: Go backend (single binary, stdlib HTTP) + SvelteKit
(Svelte 5) frontend, gallery-dl as the external extractor. See README.md for
what it does and how it's deployed.

## Commands

- `make test` — Go suite; no network, no gallery-dl, no DB service needed
  (tests swap in in-memory SQLite).
- `make test-race` / `make vet` — CI runs both, plus a `gofmt -l` gate:
  unformatted Go fails the build.
- `make run` — backend on :8787 with embedded SQLite at `data/loupe.db`.
- `cd frontend && npm run dev` — UI on :5173, proxies `/api` to :8787.
- `make build` — frontend build + `go build` in one step.
- `make sqlc` — regenerate query code after editing `internal/db/*/query.sql`.

## Layout

- `main.go` — config (env vars, `LOUPE_` prefix), gallery-dl invocation,
  polling, all HTTP handlers. `main_test.go` covers it.
- `embed.go` — serves the embedded UI (`//go:embed all:frontend/build`).
- `internal/repo/` — storage interface + per-engine adapters.
- `internal/db/{sqlite,postgres,mysql}/` — sqlc-generated; never edit
  `*.sql.go` by hand. Schema changes are goose migrations, one per engine.
- `frontend/src/` — routes in `routes/[...path]/+page.svelte`, shared state in
  `lib/state.svelte.js` (Svelte 5 runes).

## Invariants

- `frontend/build/` is a build artifact (only `.gitkeep` is committed). Run the
  frontend build before `go build` or the binary embeds a placeholder.
- Decisions are per `(source, image)` — never dedupe items globally.
- Mutating API endpoints are POST-only behind the `guard` middleware
  (JSON Content-Type + Host/Origin checks); new endpoints must follow it.
- Item URLs/metadata come from remote sites via gallery-dl: treat as untrusted.
- The UI is mobile-first; desktop-only styling lives in `@media(min-width:900px)`
  blocks in `frontend/src/app.css` and must not change mobile layout.
