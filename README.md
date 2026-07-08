# Loupe

A self-hosted, single-binary watcher for image galleries, built on
[gallery-dl](https://github.com/mikf/gallery-dl). Point it at gallery URLs; it
polls them on a schedule and shows you only the **new, unrated** images in a
swipe-to-decide UI. Keeps go to **Selects** (exportable as a URL list or zip),
passes disappear. Your decisions persist, so you only ever look at anything
once.

<p align="center">
  <img src="docs/screenshot-desktop.png" alt="Reviewing new images in Loupe: scope sidebar on the left, current card with keep/pass actions on the right" width="800">
</p>

Typical uses:

- You follow thirty artists across DeviantArt, Pixiv and ArtStation. Instead of
  opening thirty pages to find the handful of new works, review them in one
  queue on your phone over coffee.
- You collect wallpapers from a few subreddits and Flickr groups. Loupe turns
  the daily scroll into a one-minute swipe session and gives you a zip of the
  keepers.
- You track booru tag searches with hundreds of new posts a week and want the
  triage separated from the browsing.

Anything gallery-dl can extract — boorus, reddit, DeviantArt, Pixiv, Flickr,
Twitter/X, plain web galleries, [hundreds of
sites](https://github.com/mikf/gallery-dl/blob/master/docs/supportedsites.md) —
Loupe can watch.

## How it works

Loupe shells out to `gallery-dl -j` on a schedule (no scraping logic of its
own), stores item metadata in an embedded SQLite database (Postgres/MySQL
optional), and serves the UI and JSON API from one Go binary with the frontend
embedded. It stores URLs and decisions, not image files — media is loaded from
the source site; only the zip export downloads anything.

- **Sources** are gallery URLs. **Collections** group sources so you can review
  them together (interleaved round-robin, so one source's backlog never buries
  the rest).
- Decisions are **per source**: the same image in two sources is two independent
  review items.
- Editing a source's URL never deletes decided items — they're marked *stale*,
  reviewable or purgeable separately.

## Quick start

**Docker**

```sh
docker run -d --name loupe -p 127.0.0.1:8787:8787 -v loupe-data:/app/data \
  ghcr.io/johnnycube/loupe:latest
```

Open http://localhost:8787, go to **Sources**, paste a gallery URL.

**Binary** (needs `gallery-dl` on PATH, e.g. `pipx install gallery-dl`)

```sh
make build   # builds the frontend, embeds it, compiles ./loupe
./loupe      # http://localhost:8787, state in ./data
```

See [`samples/`](samples/) for a production docker-compose file, a systemd
unit, and a gallery-dl config template.

## Security

**Loupe has no authentication.** Anyone who can reach the port can review your
queue, manage sources, and read any gallery-dl credentials stored as per-source
configs. Bind it to localhost (`LOUPE_HTTP_ADDR=127.0.0.1`, or
`-p 127.0.0.1:8787:8787` in Docker) and reach it via VPN/SSH tunnel, or put an
authenticating reverse proxy in front of it.

What is in place: mutating endpoints are POST-only, require a JSON content
type, and reject cross-origin requests; a Host-header allowlist blocks DNS
rebinding (IP literals and `localhost` always pass — set `LOUPE_ALLOWED_HOSTS`
when serving via a hostname); the server never executes shell strings
(gallery-dl is invoked with an argument vector). By design, Loupe fetches
attacker-supplied-looking things: it polls whatever URLs you add and the zip
export downloads item URLs server-side — another reason not to expose it to
untrusted users.

## Configuration

All settings are `LOUPE_`-prefixed environment variables; all are optional.

| var                     | default      | meaning                                                       |
|-------------------------|--------------|---------------------------------------------------------------|
| `LOUPE_HTTP_ADDR`       | *(all)*      | bind address, e.g. `127.0.0.1`                                |
| `LOUPE_HTTP_PORT`       | `8787`       | HTTP port                                                     |
| `LOUPE_ALLOWED_HOSTS`   | *(empty)*    | comma-separated hostnames allowed in the Host header (reverse-proxy domains) |
| `LOUPE_DB_DRIVER`       | `sqlite`     | `sqlite` \| `postgres` \| `mysql`                             |
| `LOUPE_DB_DSN`          | *(embedded)* | DSN for the chosen driver; default is SQLite at `data/loupe.db` |
| `LOUPE_POLL_MINUTES`    | `15`         | how often every source is re-checked                          |
| `LOUPE_PER_SOURCE`      | `50`         | newest-N window each scheduled poll pulls                     |
| `LOUPE_RESCAN_MAX`      | `100000`     | hard cap on items a full rescan pages through                 |
| `LOUPE_PRELOAD_DEPTH`   | `3`          | images the review UI prefetches ahead                         |
| `LOUPE_GALLERY_DL`      | `gallery-dl` | path to the gallery-dl binary                                 |
| `LOUPE_GDL_TIMEOUT_SEC` | `120`        | per-request gallery-dl timeout                                |

```sh
# Postgres
LOUPE_DB_DRIVER=postgres LOUPE_DB_DSN='postgres://user:pass@host:5432/loupe?sslmode=disable'
# MySQL / MariaDB
LOUPE_DB_DRIVER=mysql LOUPE_DB_DSN='user:pass@tcp(host:3306)/loupe?parseTime=true'
```

The schema is created automatically on first start.

### gallery-dl credentials

Some sites need API keys or cookies. Loupe uses gallery-dl's own configuration,
in two layers:

1. **Global**: gallery-dl's standard config
   (`~/.config/gallery-dl/config.json`; in Docker mount it at
   `/app/.config/gallery-dl/config.json`) applies to every source. Template:
   [`samples/gallery-dl-config.example.json`](samples/gallery-dl-config.example.json).
2. **Per source**: each source can add a config file path, an inline JSON
   config, or a named shared config (managed in the UI), layered on top of the
   global one — for the one source that needs different credentials.

Poll failures (auth required, rate limit, invalid config …) surface as that
source's error in the UI.

### Polling vs. rescan

The scheduled poll fetches only the newest `LOUPE_PER_SOURCE` items — cheap,
catches what's new. Adding a source (or pressing **Rescan**) pages through the
entire gallery to backfill its history, preserving existing decisions. Full
scans run gallery-dl in windows of 1000 items; sites that paginate slowly can
exceed the default 120s window timeout — the source then shows a *timed out*
error, and raising `LOUPE_GDL_TIMEOUT_SEC` (e.g. to `600`) fixes it.

## Development

```sh
make test            # Go suite — no network, no gallery-dl needed
make run             # backend on :8787
cd frontend && npm run dev   # UI on :5173 with hot reload, proxies /api
```

The UI is SvelteKit (Svelte 5) built to static files and embedded into the Go
binary — production needs no Node. `make build` does frontend + binary in one
step; CI (`.github/workflows/docker.yaml`) tests, builds and publishes the
image to ghcr.io on pushes to `main` and `v*` tags.

## License

[AGPL-3.0](LICENSE)
