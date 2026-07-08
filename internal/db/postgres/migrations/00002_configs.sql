-- +goose Up
-- Named, reusable gallery-dl configs (see the SQLite copy for the rationale).
-- config_id is added with ALTER so pre-existing sources tables receive it.
CREATE TABLE IF NOT EXISTS configs (
    id          TEXT   PRIMARY KEY,
    name        TEXT   NOT NULL DEFAULT '',
    config_json TEXT   NOT NULL DEFAULT '',
    added_at    BIGINT NOT NULL DEFAULT 0
);

ALTER TABLE sources ADD COLUMN IF NOT EXISTS config_id TEXT NOT NULL DEFAULT '';

-- +goose Down
ALTER TABLE sources DROP COLUMN IF EXISTS config_id;
DROP TABLE IF EXISTS configs;
