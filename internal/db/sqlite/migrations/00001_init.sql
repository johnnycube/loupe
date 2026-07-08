-- +goose Up
-- Baseline schema for the SQLite backend. Idempotent (CREATE ... IF NOT EXISTS)
-- so databases created before goose was adopted are brought under version
-- tracking without re-running their DDL: goose records this as applied while the
-- existing tables are left untouched. Lowest-common-denominator types only (TEXT
-- and BIGINT) so sqlc emits plain string/int64 across all three engines.
CREATE TABLE IF NOT EXISTS sources (
    id          TEXT   PRIMARY KEY,
    name        TEXT   NOT NULL DEFAULT '',
    description TEXT   NOT NULL DEFAULT '',
    url         TEXT   NOT NULL DEFAULT '',
    config_file TEXT   NOT NULL DEFAULT '',
    config_json TEXT   NOT NULL DEFAULT '',
    added_at    BIGINT NOT NULL DEFAULT 0,
    last_poll   BIGINT NOT NULL DEFAULT 0,
    last_added  BIGINT NOT NULL DEFAULT 0,
    last_error  TEXT   NOT NULL DEFAULT ''
);

CREATE TABLE IF NOT EXISTS collections (
    id          TEXT   PRIMARY KEY,
    name        TEXT   NOT NULL DEFAULT '',
    description TEXT   NOT NULL DEFAULT '',
    source_ids  TEXT   NOT NULL DEFAULT '[]', -- JSON array of source ids
    added_at    BIGINT NOT NULL DEFAULT 0
);

CREATE TABLE IF NOT EXISTS items (
    id         TEXT   PRIMARY KEY,
    source_id  TEXT   NOT NULL DEFAULT '',
    label      TEXT   NOT NULL DEFAULT '',
    image_key  TEXT   NOT NULL DEFAULT '',
    url        TEXT   NOT NULL DEFAULT '',
    sample     TEXT   NOT NULL DEFAULT '',
    title      TEXT   NOT NULL DEFAULT '',
    status     TEXT   NOT NULL DEFAULT 'new', -- new | good | bad
    gone       BIGINT NOT NULL DEFAULT 0,     -- 0/1
    added_at   BIGINT NOT NULL DEFAULT 0,
    last_seen  BIGINT NOT NULL DEFAULT 0,
    decided_at BIGINT NOT NULL DEFAULT 0
);

CREATE INDEX IF NOT EXISTS idx_items_status_gone ON items (status, gone);
CREATE INDEX IF NOT EXISTS idx_items_source ON items (source_id);

CREATE TABLE IF NOT EXISTS app_state (
    id         BIGINT PRIMARY KEY, -- always 1
    last_poll  BIGINT NOT NULL DEFAULT 0,
    last_added BIGINT NOT NULL DEFAULT 0
);

-- +goose Down
DROP TABLE IF EXISTS app_state;
DROP TABLE IF EXISTS items;
DROP TABLE IF EXISTS collections;
DROP TABLE IF EXISTS sources;
