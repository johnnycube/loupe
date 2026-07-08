-- +goose Up
-- Baseline schema for the MySQL/MariaDB backend. Idempotent (CREATE ... IF NOT
-- EXISTS) so databases created before goose was adopted are brought under
-- version tracking without re-running their DDL. Key/indexed columns are
-- VARCHAR(191) (the utf8mb4-safe single-column index width) since MySQL cannot
-- index a TEXT column without a prefix length; indexes are declared inline
-- because MySQL lacks CREATE INDEX IF NOT EXISTS.
CREATE TABLE IF NOT EXISTS sources (
    id          VARCHAR(191) PRIMARY KEY,
    name        TEXT   NOT NULL,
    description TEXT   NOT NULL,
    url         TEXT   NOT NULL,
    config_file TEXT   NOT NULL,
    config_json TEXT   NOT NULL,
    added_at    BIGINT NOT NULL DEFAULT 0,
    last_poll   BIGINT NOT NULL DEFAULT 0,
    last_added  BIGINT NOT NULL DEFAULT 0,
    last_error  TEXT   NOT NULL
);

CREATE TABLE IF NOT EXISTS collections (
    id          VARCHAR(191) PRIMARY KEY,
    name        TEXT   NOT NULL,
    description TEXT   NOT NULL,
    source_ids  TEXT   NOT NULL, -- JSON array of source ids
    added_at    BIGINT NOT NULL DEFAULT 0
);

CREATE TABLE IF NOT EXISTS items (
    id         VARCHAR(191) PRIMARY KEY,
    source_id  VARCHAR(191) NOT NULL DEFAULT '',
    label      TEXT   NOT NULL,
    image_key  TEXT   NOT NULL,
    url        TEXT   NOT NULL,
    sample     TEXT   NOT NULL,
    title      TEXT   NOT NULL,
    status     VARCHAR(16) NOT NULL DEFAULT 'new', -- new | good | bad
    gone       BIGINT NOT NULL DEFAULT 0,          -- 0/1
    added_at   BIGINT NOT NULL DEFAULT 0,
    last_seen  BIGINT NOT NULL DEFAULT 0,
    decided_at BIGINT NOT NULL DEFAULT 0,
    KEY idx_items_status_gone (status, gone),
    KEY idx_items_source (source_id)
);

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
