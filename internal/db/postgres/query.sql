-- PostgreSQL queries. Same query names as the SQLite and MySQL files; Postgres
-- uses $n placeholders.

/* ---------------------------------------------------------------- items */

-- name: ListReviewable :many
SELECT * FROM items WHERE status = 'new' AND gone = 0;

-- name: ListStaleNew :many
SELECT * FROM items WHERE gone = 1 AND status = 'new';

-- name: ListGood :many
SELECT * FROM items WHERE status = 'good' ORDER BY decided_at DESC;

-- name: GetItem :one
SELECT * FROM items WHERE id = $1;

-- name: InsertItemIfNew :execrows
INSERT INTO items (id, source_id, label, image_key, url, sample, title, status, gone, added_at, last_seen, decided_at)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12)
ON CONFLICT(id) DO NOTHING;

-- name: UnstaleItem :execrows
UPDATE items SET gone = 0, last_seen = $1 WHERE id = $2;

-- name: DecideItem :execrows
UPDATE items SET status = $1, decided_at = $2 WHERE id = $3;

-- name: SetItemStatus :execrows
UPDATE items SET status = $1 WHERE id = $2;

-- name: ResetItem :execrows
UPDATE items SET status = 'new', decided_at = 0 WHERE id = $1;

-- name: MarkSourceGone :exec
UPDATE items SET gone = 1 WHERE source_id = $1;

-- name: PurgeStale :execrows
DELETE FROM items WHERE source_id = $1 AND gone = 1 AND status <> 'good';

-- name: DeleteNewBySource :exec
DELETE FROM items WHERE source_id = $1 AND status = 'new';

-- name: CountByStatusGone :many
SELECT status, gone, COUNT(*) AS n FROM items GROUP BY status, gone;

-- name: CountBySourceStatusGone :many
SELECT source_id, status, gone, COUNT(*) AS n FROM items GROUP BY source_id, status, gone;

/* ---------------------------------------------------------------- sources */

-- name: ListSources :many
SELECT * FROM sources;

-- name: GetSource :one
SELECT * FROM sources WHERE id = $1;

-- name: InsertSource :exec
INSERT INTO sources (id, name, description, url, config_file, config_json, config_id, added_at, last_poll, last_added, last_error)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11);

-- name: UpdateSource :exec
UPDATE sources SET name = $1, description = $2, url = $3, config_file = $4, config_json = $5, config_id = $6,
    last_poll = $7, last_added = $8, last_error = $9 WHERE id = $10;

-- name: MarkPolled :exec
UPDATE sources SET last_poll = $1, last_added = $2, last_error = '' WHERE id = $3;

-- name: SetSourceError :exec
UPDATE sources SET last_error = $1, last_poll = $2 WHERE id = $3;

-- name: DeleteSource :exec
DELETE FROM sources WHERE id = $1;

/* ------------------------------------------------------------ collections */

-- name: ListCollections :many
SELECT * FROM collections;

-- name: GetCollection :one
SELECT * FROM collections WHERE id = $1;

-- name: InsertCollection :exec
INSERT INTO collections (id, name, description, source_ids, added_at)
VALUES ($1, $2, $3, $4, $5);

-- name: UpdateCollection :exec
UPDATE collections SET name = $1, description = $2, source_ids = $3 WHERE id = $4;

-- name: DeleteCollection :exec
DELETE FROM collections WHERE id = $1;

/* ---------------------------------------------------------------- configs */

-- name: ListConfigs :many
SELECT * FROM configs ORDER BY name;

-- name: GetConfig :one
SELECT * FROM configs WHERE id = $1;

-- name: InsertConfig :exec
INSERT INTO configs (id, name, config_json, added_at) VALUES ($1, $2, $3, $4);

-- name: UpdateConfig :exec
UPDATE configs SET name = $1, config_json = $2 WHERE id = $3;

-- name: DeleteConfig :exec
DELETE FROM configs WHERE id = $1;

/* ---------------------------------------------------------------- state */

-- name: GetState :one
SELECT last_poll, last_added FROM app_state WHERE id = 1;

-- name: SetState :exec
INSERT INTO app_state (id, last_poll, last_added) VALUES (1, $1, $2)
ON CONFLICT(id) DO UPDATE SET last_poll = excluded.last_poll, last_added = excluded.last_added;
