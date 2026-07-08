-- +goose Up
-- Named, reusable gallery-dl configs. A source may reference one by id via
-- sources.config_id; the referenced config's JSON is resolved at poll time, so
-- editing a config updates every source that points at it. config_id is added
-- with ALTER (never folded into the 00001 CREATE) so existing sources tables,
-- which predate this column, actually receive it.
CREATE TABLE IF NOT EXISTS configs (
    id          TEXT   PRIMARY KEY,
    name        TEXT   NOT NULL DEFAULT '',
    config_json TEXT   NOT NULL DEFAULT '',
    added_at    BIGINT NOT NULL DEFAULT 0
);

ALTER TABLE sources ADD COLUMN config_id TEXT NOT NULL DEFAULT '';

-- +goose Down
ALTER TABLE sources DROP COLUMN config_id;
DROP TABLE IF EXISTS configs;
