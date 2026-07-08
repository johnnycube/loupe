-- MySQL / MariaDB queries. Same query names as the SQLite and Postgres files;
-- MySQL uses ? placeholders, INSERT IGNORE and ON DUPLICATE KEY UPDATE for the
-- upserts (it has no ON CONFLICT).

/* ---------------------------------------------------------------- items */

-- name: ListReviewable :many
SELECT * FROM items WHERE status = 'new' AND gone = 0;

-- name: ListStaleNew :many
SELECT * FROM items WHERE gone = 1 AND status = 'new';

-- name: ListGood :many
SELECT * FROM items WHERE status = 'good' ORDER BY decided_at DESC;

-- name: GetItem :one
SELECT * FROM items WHERE id = ?;

-- name: InsertItemIfNew :execrows
INSERT IGNORE INTO items (id, source_id, label, image_key, url, sample, title, status, gone, added_at, last_seen, decided_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?);

-- name: UnstaleItem :execrows
UPDATE items SET gone = 0, last_seen = ? WHERE id = ?;

-- name: DecideItem :execrows
UPDATE items SET status = ?, decided_at = ? WHERE id = ?;

-- name: SetItemStatus :execrows
UPDATE items SET status = ? WHERE id = ?;

-- name: ResetItem :execrows
UPDATE items SET status = 'new', decided_at = 0 WHERE id = ?;

-- name: MarkSourceGone :exec
UPDATE items SET gone = 1 WHERE source_id = ?;

-- name: PurgeStale :execrows
DELETE FROM items WHERE source_id = ? AND gone = 1 AND status <> 'good';

-- name: DeleteNewBySource :exec
DELETE FROM items WHERE source_id = ? AND status = 'new';

-- name: CountByStatusGone :many
SELECT status, gone, COUNT(*) AS n FROM items GROUP BY status, gone;

-- name: CountBySourceStatusGone :many
SELECT source_id, status, gone, COUNT(*) AS n FROM items GROUP BY source_id, status, gone;

/* ---------------------------------------------------------------- sources */

-- name: ListSources :many
SELECT * FROM sources;

-- name: GetSource :one
SELECT * FROM sources WHERE id = ?;

-- name: InsertSource :exec
INSERT INTO sources (id, name, description, url, config_file, config_json, config_id, added_at, last_poll, last_added, last_error)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?);

-- name: UpdateSource :exec
UPDATE sources SET name = ?, description = ?, url = ?, config_file = ?, config_json = ?, config_id = ?,
    last_poll = ?, last_added = ?, last_error = ? WHERE id = ?;

-- name: MarkPolled :exec
UPDATE sources SET last_poll = ?, last_added = ?, last_error = '' WHERE id = ?;

-- name: SetSourceError :exec
UPDATE sources SET last_error = ?, last_poll = ? WHERE id = ?;

-- name: DeleteSource :exec
DELETE FROM sources WHERE id = ?;

/* ------------------------------------------------------------ collections */

-- name: ListCollections :many
SELECT * FROM collections;

-- name: GetCollection :one
SELECT * FROM collections WHERE id = ?;

-- name: InsertCollection :exec
INSERT INTO collections (id, name, description, source_ids, added_at)
VALUES (?, ?, ?, ?, ?);

-- name: UpdateCollection :exec
UPDATE collections SET name = ?, description = ?, source_ids = ? WHERE id = ?;

-- name: DeleteCollection :exec
DELETE FROM collections WHERE id = ?;

/* ---------------------------------------------------------------- configs */

-- name: ListConfigs :many
SELECT * FROM configs ORDER BY name;

-- name: GetConfig :one
SELECT * FROM configs WHERE id = ?;

-- name: InsertConfig :exec
INSERT INTO configs (id, name, config_json, added_at) VALUES (?, ?, ?, ?);

-- name: UpdateConfig :exec
UPDATE configs SET name = ?, config_json = ? WHERE id = ?;

-- name: DeleteConfig :exec
DELETE FROM configs WHERE id = ?;

/* ---------------------------------------------------------------- state */

-- name: GetState :one
SELECT last_poll, last_added FROM app_state WHERE id = 1;

-- name: SetState :exec
INSERT INTO app_state (id, last_poll, last_added) VALUES (1, ?, ?)
ON DUPLICATE KEY UPDATE last_poll = VALUES(last_poll), last_added = VALUES(last_added);
