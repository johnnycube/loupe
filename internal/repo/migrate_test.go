package repo

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"

	_ "modernc.org/sqlite"
)

// TestMigrateLegacyDatabase proves the goose switch is backward compatible: a
// database created before goose existed (the old tables, no goose_db_version,
// no sources.config_id) must come under version tracking on Open without losing
// data, and must gain the configs feature from migration 00002.
func TestMigrateLegacyDatabase(t *testing.T) {
	path := filepath.Join(t.TempDir(), "legacy.db")

	// 1. Hand-build a pre-goose database with a real source row.
	raw, err := sql.Open("sqlite", "file:"+path)
	if err != nil {
		t.Fatal(err)
	}
	for _, ddl := range []string{
		`CREATE TABLE sources (id TEXT PRIMARY KEY, name TEXT NOT NULL DEFAULT '', description TEXT NOT NULL DEFAULT '', url TEXT NOT NULL DEFAULT '', config_file TEXT NOT NULL DEFAULT '', config_json TEXT NOT NULL DEFAULT '', added_at BIGINT NOT NULL DEFAULT 0, last_poll BIGINT NOT NULL DEFAULT 0, last_added BIGINT NOT NULL DEFAULT 0, last_error TEXT NOT NULL DEFAULT '')`,
		`CREATE TABLE collections (id TEXT PRIMARY KEY, name TEXT NOT NULL DEFAULT '', description TEXT NOT NULL DEFAULT '', source_ids TEXT NOT NULL DEFAULT '[]', added_at BIGINT NOT NULL DEFAULT 0)`,
		`CREATE TABLE items (id TEXT PRIMARY KEY, source_id TEXT NOT NULL DEFAULT '', label TEXT NOT NULL DEFAULT '', image_key TEXT NOT NULL DEFAULT '', url TEXT NOT NULL DEFAULT '', sample TEXT NOT NULL DEFAULT '', title TEXT NOT NULL DEFAULT '', status TEXT NOT NULL DEFAULT 'new', gone BIGINT NOT NULL DEFAULT 0, added_at BIGINT NOT NULL DEFAULT 0, last_seen BIGINT NOT NULL DEFAULT 0, decided_at BIGINT NOT NULL DEFAULT 0)`,
		`CREATE TABLE app_state (id BIGINT PRIMARY KEY, last_poll BIGINT NOT NULL DEFAULT 0, last_added BIGINT NOT NULL DEFAULT 0)`,
		`INSERT INTO sources (id, name, url) VALUES ('s1', 'Legacy Source', 'https://example.com/g')`,
	} {
		if _, err := raw.Exec(ddl); err != nil {
			t.Fatalf("seed legacy db: %v", err)
		}
	}
	raw.Close()

	// 2. Open through the repo: goose baselines at 00001 (idempotent, existing
	//    tables untouched) then applies 00002 (configs + sources.config_id).
	r, err := Open("sqlite", "file:"+path)
	if err != nil {
		t.Fatalf("open/migrate legacy db: %v", err)
	}
	defer r.Close()
	ctx := context.Background()

	// 3. The legacy row survived and now exposes the new (empty) config_id.
	s, ok, err := r.GetSource(ctx, "s1")
	if err != nil || !ok {
		t.Fatalf("legacy source lost after migrate: ok=%v err=%v", ok, err)
	}
	if s.Name != "Legacy Source" || s.ConfigID != "" {
		t.Fatalf("legacy source corrupted: %+v", s)
	}

	// 4. The configs feature from 00002 is usable end to end.
	if err := r.InsertConfig(ctx, &Config{ID: "c1", Name: "reddit", ConfigJSON: `{"extractor":{}}`, AddedAt: 1}); err != nil {
		t.Fatalf("insert config: %v", err)
	}
	s.ConfigID = "c1"
	if err := r.UpdateSource(ctx, s); err != nil {
		t.Fatalf("reference config from source: %v", err)
	}
	got, _, _ := r.GetSource(ctx, "s1")
	if got.ConfigID != "c1" {
		t.Fatalf("config reference not persisted: %q", got.ConfigID)
	}
}
