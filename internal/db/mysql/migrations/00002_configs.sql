-- +goose Up
-- Named, reusable gallery-dl configs (see the SQLite copy for the rationale).
-- config_id is added with ALTER so pre-existing sources tables receive it; it is
-- VARCHAR(191) rather than TEXT because MySQL TEXT columns cannot take a DEFAULT.
CREATE TABLE IF NOT EXISTS configs (
    id          VARCHAR(191) PRIMARY KEY,
    name        TEXT   NOT NULL,
    config_json TEXT   NOT NULL,
    added_at    BIGINT NOT NULL DEFAULT 0
);

ALTER TABLE sources ADD COLUMN config_id VARCHAR(191) NOT NULL DEFAULT '';

-- +goose Down
ALTER TABLE sources DROP COLUMN config_id;
DROP TABLE IF EXISTS configs;
