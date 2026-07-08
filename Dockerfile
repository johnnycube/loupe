# syntax=docker/dockerfile:1
#
# Production image for Loupe: build the SvelteKit UI, embed it into the Go
# binary (//go:embed all:frontend/build), and ship one static executable plus
# gallery-dl at runtime. No Node, no Go toolchain in the final image.

# ---- 1. build the SvelteKit static UI ----------------------------------------
# Base images are pinned to an explicit version AND digest for reproducible
# builds: the version tag documents what we run, the digest enforces it. Bump
# both together when updating.
FROM node:24.18.0-alpine3.24@sha256:a0b9bf06e4e6193cf7a0f58816cc935ff8c2a908f81e6f1a95432d679c54fbfd AS frontend
WORKDIR /app/frontend
# Install deps from the lockfile first so this layer caches across UI edits.
COPY frontend/package.json frontend/package-lock.json ./
RUN npm ci
COPY frontend/ ./
RUN npm run build          # -> /app/frontend/build (static, embedded next)

# ---- 2. compile the single Go binary (embeds the UI) -------------------------
FROM golang:1.26.4-alpine3.24@sha256:3ad57304ad93bbec8548a0437ad9e06a455660655d9af011d58b993f6f615648 AS backend
WORKDIR /src
# Download modules first so this layer caches across source edits.
COPY go.mod go.sum ./
RUN go mod download
COPY *.go ./
COPY internal ./internal
# embed.go expects the built UI at ./frontend/build at compile time.
COPY --from=frontend /app/frontend/build ./frontend/build
# Build metadata for the About page. The .git dir is not in the build context,
# so the Go toolchain can't auto-stamp VCS info (vcs.revision/time) the way it
# does for `go build` in a working tree — the CI pipeline passes these in as
# build args and we inject them via -ldflags (matching the Makefile's LDFLAGS).
# Declared here, right before the build RUN, so they don't bust the cache of the
# `go mod download` layer above. They stay empty for a plain `docker build`.
ARG GIT_COMMIT=""
ARG GIT_TAG=""
ARG BUILD_TIME=""
# CGO off -> a fully static binary (all DB drivers are pure Go) that runs on the
# slim runtime image.
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath \
    -ldflags="-s -w -X main.gitCommit=${GIT_COMMIT} -X main.gitTag=${GIT_TAG} -X main.buildTime=${BUILD_TIME}" \
    -o /out/loupe .

# ---- 3. runtime: slim image + gallery-dl -------------------------------------
FROM python:3.14.6-alpine3.24@sha256:26730869004e2b9c4b9ad09cab8625e81d256d1ce97e72df5520e806b1709f92
# gallery-dl is the runtime extractor; ffmpeg lets it mux some video sources.
# Versions are pinned for reproducibility — bump deliberately, not by drift.
RUN apk add --no-cache ffmpeg=8.1.2-r0 ca-certificates=20260611-r0 \
    && pip install --no-cache-dir gallery-dl==1.32.3 \
    && adduser -D -h /app loupe
WORKDIR /app
COPY --from=backend /out/loupe /usr/local/bin/loupe
# State (sources, items, decisions) lives in ./data — persist it.
RUN mkdir -p /app/data && chown -R loupe:loupe /app
USER loupe
# All Loupe settings use the LOUPE_ prefix (see README "Config").
ENV LOUPE_HTTP_PORT=8787
EXPOSE 8787
VOLUME ["/app/data"]
# Per-source gallery-dl config / credentials go in gallery-dl's own config,
# e.g. mount one at /app/.config/gallery-dl/config.json (HOME is /app).
HEALTHCHECK --interval=30s --timeout=4s --start-period=10s \
    CMD wget -qO- http://localhost:8787/api/stats >/dev/null 2>&1 || exit 1
ENTRYPOINT ["loupe"]
