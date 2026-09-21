# syntax=docker/dockerfile:1
#
# Production image for Loupe: build the SvelteKit UI, embed it into the Go
# binary (//go:embed all:frontend/build), and ship one static executable plus
# gallery-dl at runtime. No Node, no Go toolchain in the final image.

# ---- 1. build the SvelteKit static UI ----------------------------------------
# Base images are pinned to an explicit version AND digest for reproducible
# builds: the version tag documents what we run, the digest enforces it. Bump
# both together when updating.
FROM node:24.21.0-alpine3.24@sha256:ebfe2f90462722a7a4de65e91990e97fe0d401c70e0e762c5b53302f905ec1c1 AS frontend
WORKDIR /app/frontend
# Install deps from the lockfile first so this layer caches across UI edits.
COPY frontend/package.json frontend/package-lock.json ./
RUN npm ci
COPY frontend/ ./
RUN npm run build          # -> /app/frontend/build (static, embedded next)

# ---- 2. compile the single Go binary (embeds the UI) -------------------------
FROM golang:1.27.1-alpine3.24@sha256:8a5910f31396cd4d89662f56c68b3ae31d374308270a1c3bd96672ee5ed43414 AS backend
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
FROM python:3.14.7-alpine3.24@sha256:016508ba505da24f7139765bc4bb669df4e88eb2f12eeadd571bf2f88d7533df
# gallery-dl is the runtime extractor; ffmpeg lets it mux some video sources.
# Versions are pinned for reproducibility — bump deliberately, not by drift.
RUN apk add --no-cache ffmpeg=8.1.2-r0 ca-certificates=20260909-r0 \
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
