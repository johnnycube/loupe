# Architecture

Loupe is one Go binary that polls image galleries through gallery-dl, stores
what it finds in a database, and serves a swipe-to-decide UI over the result.
This page is the map: components, boundaries, data flow, and the decisions
that shaped them. For what it does and how to run it, see the README.

## Components

```
 remote gallery sites
        ▲  HTTP (by gallery-dl)
        │
 ┌──────┴──────────┐   argv, JSON on stdout    ┌───────────────────────────────┐
 │   gallery-dl    │◄──────────────────────────│  loupe (Go, single binary)    │
 │  (external CLI) │                            │                               │
 └─────────────────┘                            │  poller ── parse ── merge     │
                                                │     │                 │       │
 ┌─────────────────┐   JSON over /api/*         │  HTTP API (stdlib) ── repo ───┼──► SQLite (embedded)
 │  SvelteKit SPA  │◄──────────────────────────►│     │                 │       │    Postgres / MySQL
 │  (embedded)     │                            │  embedded UI (go:embed)       │    (optional)
 └─────────────────┘                            └───────────────────────────────┘
```

| component | where | role |
|---|---|---|
| gallery-dl | external binary on `PATH` (`LOUPE_GALLERY_DL`) | the only extractor; invoked as `gallery-dl -j <url>` with an argument vector, never a shell string |
| poller | `main.go` (`pollAll`, `pollSourceSnapshot`, `pollSourceFull`) | runs every `LOUPE_POLL_MINUTES`; a cheap newest-N snapshot per source, or a full paged rescan on demand |
| parser / merger | `main.go` (`parseDump`, `mergeExtracted`) | turns gallery-dl's JSON dump into items and inserts the ones not yet known for that source |
| HTTP API | `main.go` (`handle*`, `guard`) | stdlib `net/http`; every mutating endpoint is POST-only behind the `guard` middleware |
| repo | `internal/repo` | the storage interface plus one adapter per engine; all SQL is sqlc-generated from `internal/db/<engine>/query.sql` |
| migrations | `internal/db/<engine>/migrations` | goose migrations, one set per engine, applied at start-up |
| UI | `frontend/` → embedded via `embed.go` | SvelteKit (Svelte 5 runes) built to static files; a single SPA route with client-side navigation |

## Data model

- **Source** — a gallery URL plus a name, optional gallery-dl config (file,
  inline JSON, or a shared named **Config**), poll bookkeeping and last error.
- **Collection** — an ordered group of sources reviewed together.
- **Item** — one image *in one source*: `(source, imageKey)` is the identity,
  `status` is `new | good | bad`, `gone` marks items that disappeared from
  their source (*stale*). Items store URLs and metadata only, never files.
- **State** — last poll time and count, for the stats view.

## Data flow

1. **Poll.** The scheduler (or a user action) calls gallery-dl for a source.
   Per-source config is layered on top of gallery-dl's own global config by
   staging temp files and passing them as config arguments.
2. **Parse.** The JSON dump is reduced to `(url, sample, title, imageKey)`;
   `imageKey` prefers a stable id (`md5`, `id`) and falls back to the URL.
   Everything here is untrusted input from a remote site.
3. **Merge.** Each extracted item is inserted if `(source, imageKey)` is new;
   known items are refreshed (un-staled) rather than duplicated. A full rescan
   additionally marks items no longer present as *gone*.
4. **Review.** `/api/next` returns reviewable items for a scope (all, stale,
   one source, one collection), interleaved round-robin across sources so one
   backlog cannot bury the rest. The UI prefetches a few images ahead.
5. **Decide.** A vote sets the item's status; *good* items appear under
   **Selects**, exportable as a URL list or a zip (the one place Loupe
   downloads media, server-side, from the item URLs).

## Boundaries and trust

- **No authentication.** The deployment boundary is the network: bind to
  localhost or put an authenticating proxy in front. The README's Security
  section spells this out.
- **Request guard.** Mutations are POST with a JSON content type; `Origin` must
  match and `Host` must be an IP literal, `localhost`, or listed in
  `LOUPE_ALLOWED_HOSTS` (DNS-rebinding defence).
- **Untrusted metadata.** Item URLs, titles and keys come from remote sites via
  gallery-dl. They are stored and rendered as data; zip entry names are
  derived defensively; nothing from them reaches a shell.
- **Process boundary.** gallery-dl runs as a child process with a timeout
  (`LOUPE_GDL_TIMEOUT_SEC`); its failures surface as that source's error.

## Decisions that shaped it

1. **Shell out to gallery-dl instead of writing extractors.** Hundreds of sites
   for free, maintained by someone else; the cost is a runtime dependency and
   parsing a JSON dump whose shape varies per site (hence the key fallbacks).
2. **Store URLs and decisions, not files.** Loupe is a triage tool, not an
   archive. Media is loaded from the source at review time; only the explicit
   zip export downloads anything. Keeps the footprint tiny and the data
   portable across engines.
3. **Identity is `(source, image)`, and nothing is ever deduplicated
   globally.** The same image in two sources is two decisions; editing a
   source's URL marks items *stale* instead of deleting them. This is the
   invariant the schema, the merge step and the tests protect.
4. **One static binary, SQLite by default.** The UI is embedded at build time
   and all database drivers are pure Go, so deployment is a single file (or a
   small container). Postgres and MySQL are adapters behind the same `Repo`
   interface, with sqlc keeping the per-engine SQL honest.

## Where to look

- `main.go` — config, gallery-dl invocation, polling, every HTTP handler.
- `internal/repo/repo.go` — the `Repo` interface and the model structs.
- `internal/db/<engine>/query.sql` — the SQL; `make sqlc` regenerates the code.
- `frontend/src/routes/[...path]/+page.svelte` — the UI; shared state in
  `frontend/src/lib/state.svelte.js`.
- `AGENTS.md` — invariants and commands, kept short for humans and agents.
