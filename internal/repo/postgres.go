package repo

import (
	"context"
	"database/sql"
	"errors"

	db "github.com/johnnycube/loupe/internal/db/postgres"
)

// postgresRepo adapts the generated postgres.Queries to the Repo interface. It is
// line-for-line identical to sqliteRepo/mysqlRepo except for the imported
// generated package — the LCD schema guarantees matching signatures.
type postgresRepo struct {
	db *sql.DB
	q  *db.Queries
}

func newPostgresRepo(sqlDB *sql.DB) (Repo, error) {
	if err := runMigrations(sqlDB, db.Migrations, "postgres"); err != nil {
		return nil, err
	}
	return &postgresRepo{db: sqlDB, q: db.New(sqlDB)}, nil
}

func (r *postgresRepo) Close() error { return r.db.Close() }

func (r *postgresRepo) WithinTx(ctx context.Context, fn func(Repo) error) error {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	if err := fn(&postgresRepo{db: r.db, q: r.q.WithTx(tx)}); err != nil {
		_ = tx.Rollback()
		return err
	}
	return tx.Commit()
}

/* ---- mappers ---- */

func pgToItem(it db.Item) *Item {
	return &Item{
		ID: it.ID, SourceID: it.SourceID, Label: it.Label, ImageKey: it.ImageKey,
		URL: it.Url, Sample: it.Sample, Title: it.Title, Status: it.Status,
		Gone: i2b(it.Gone), AddedAt: it.AddedAt, LastSeen: it.LastSeen, DecidedAt: it.DecidedAt,
	}
}

func pgToSource(s db.Source) *Source {
	return &Source{
		ID: s.ID, Name: s.Name, Description: s.Description, URL: s.Url,
		ConfigFile: s.ConfigFile, ConfigJSON: s.ConfigJson, ConfigID: s.ConfigID, AddedAt: s.AddedAt,
		LastPoll: s.LastPoll, LastAdded: int(s.LastAdded), LastError: s.LastError,
	}
}

func pgToConfig(c db.Config) *Config {
	return &Config{ID: c.ID, Name: c.Name, ConfigJSON: c.ConfigJson, AddedAt: c.AddedAt}
}

func pgToCollection(c db.Collection) *Collection {
	return &Collection{
		ID: c.ID, Name: c.Name, Description: c.Description,
		SourceIDs: decodeIDs(c.SourceIds), AddedAt: c.AddedAt,
	}
}

/* ---- items ---- */

func (r *postgresRepo) ListReviewable(ctx context.Context) ([]*Item, error) {
	rows, err := r.q.ListReviewable(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]*Item, len(rows))
	for i, row := range rows {
		out[i] = pgToItem(row)
	}
	return out, nil
}

func (r *postgresRepo) ListStaleNew(ctx context.Context) ([]*Item, error) {
	rows, err := r.q.ListStaleNew(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]*Item, len(rows))
	for i, row := range rows {
		out[i] = pgToItem(row)
	}
	return out, nil
}

func (r *postgresRepo) ListGood(ctx context.Context) ([]*Item, error) {
	rows, err := r.q.ListGood(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]*Item, len(rows))
	for i, row := range rows {
		out[i] = pgToItem(row)
	}
	return out, nil
}

func (r *postgresRepo) GetItem(ctx context.Context, id string) (*Item, bool, error) {
	row, err := r.q.GetItem(ctx, id)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	return pgToItem(row), true, nil
}

func (r *postgresRepo) InsertItemIfNew(ctx context.Context, it *Item) (bool, error) {
	n, err := r.q.InsertItemIfNew(ctx, db.InsertItemIfNewParams{
		ID: it.ID, SourceID: it.SourceID, Label: it.Label, ImageKey: it.ImageKey,
		Url: it.URL, Sample: it.Sample, Title: it.Title, Status: it.Status,
		Gone: b2i(it.Gone), AddedAt: it.AddedAt, LastSeen: it.LastSeen, DecidedAt: it.DecidedAt,
	})
	return n > 0, err
}

func (r *postgresRepo) UnstaleItem(ctx context.Context, id string, lastSeen int64) error {
	_, err := r.q.UnstaleItem(ctx, db.UnstaleItemParams{LastSeen: lastSeen, ID: id})
	return err
}

func (r *postgresRepo) DecideItem(ctx context.Context, id, status string, decidedAt int64) (bool, error) {
	n, err := r.q.DecideItem(ctx, db.DecideItemParams{Status: status, DecidedAt: decidedAt, ID: id})
	return n > 0, err
}

func (r *postgresRepo) SetItemStatus(ctx context.Context, id, status string) (bool, error) {
	n, err := r.q.SetItemStatus(ctx, db.SetItemStatusParams{Status: status, ID: id})
	return n > 0, err
}

func (r *postgresRepo) ResetItem(ctx context.Context, id string) (bool, error) {
	n, err := r.q.ResetItem(ctx, id)
	return n > 0, err
}

func (r *postgresRepo) MarkSourceGone(ctx context.Context, sourceID string) error {
	return r.q.MarkSourceGone(ctx, sourceID)
}

func (r *postgresRepo) PurgeStale(ctx context.Context, sourceID string) (int, error) {
	n, err := r.q.PurgeStale(ctx, sourceID)
	return int(n), err
}

func (r *postgresRepo) DeleteNewBySource(ctx context.Context, sourceID string) error {
	return r.q.DeleteNewBySource(ctx, sourceID)
}

func (r *postgresRepo) CountByStatusGone(ctx context.Context) ([]StatusGoneCount, error) {
	rows, err := r.q.CountByStatusGone(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]StatusGoneCount, len(rows))
	for i, row := range rows {
		out[i] = StatusGoneCount{Status: row.Status, Gone: i2b(row.Gone), N: int(row.N)}
	}
	return out, nil
}

func (r *postgresRepo) CountBySourceStatusGone(ctx context.Context) ([]SourceStatusGoneCount, error) {
	rows, err := r.q.CountBySourceStatusGone(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]SourceStatusGoneCount, len(rows))
	for i, row := range rows {
		out[i] = SourceStatusGoneCount{SourceID: row.SourceID, Status: row.Status, Gone: i2b(row.Gone), N: int(row.N)}
	}
	return out, nil
}

/* ---- sources ---- */

func (r *postgresRepo) ListSources(ctx context.Context) ([]*Source, error) {
	rows, err := r.q.ListSources(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]*Source, len(rows))
	for i, row := range rows {
		out[i] = pgToSource(row)
	}
	return out, nil
}

func (r *postgresRepo) GetSource(ctx context.Context, id string) (*Source, bool, error) {
	row, err := r.q.GetSource(ctx, id)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	return pgToSource(row), true, nil
}

func (r *postgresRepo) InsertSource(ctx context.Context, s *Source) error {
	return r.q.InsertSource(ctx, db.InsertSourceParams{
		ID: s.ID, Name: s.Name, Description: s.Description, Url: s.URL,
		ConfigFile: s.ConfigFile, ConfigJson: s.ConfigJSON, ConfigID: s.ConfigID, AddedAt: s.AddedAt,
		LastPoll: s.LastPoll, LastAdded: int64(s.LastAdded), LastError: s.LastError,
	})
}

func (r *postgresRepo) UpdateSource(ctx context.Context, s *Source) error {
	return r.q.UpdateSource(ctx, db.UpdateSourceParams{
		Name: s.Name, Description: s.Description, Url: s.URL, ConfigFile: s.ConfigFile,
		ConfigJson: s.ConfigJSON, ConfigID: s.ConfigID, LastPoll: s.LastPoll, LastAdded: int64(s.LastAdded),
		LastError: s.LastError, ID: s.ID,
	})
}

func (r *postgresRepo) MarkPolled(ctx context.Context, id string, lastPoll int64, lastAdded int) error {
	return r.q.MarkPolled(ctx, db.MarkPolledParams{LastPoll: lastPoll, LastAdded: int64(lastAdded), ID: id})
}

func (r *postgresRepo) SetSourceError(ctx context.Context, id, msg string, lastPoll int64) error {
	return r.q.SetSourceError(ctx, db.SetSourceErrorParams{LastError: msg, LastPoll: lastPoll, ID: id})
}

func (r *postgresRepo) DeleteSource(ctx context.Context, id string) error {
	return r.q.DeleteSource(ctx, id)
}

/* ---- collections ---- */

func (r *postgresRepo) ListCollections(ctx context.Context) ([]*Collection, error) {
	rows, err := r.q.ListCollections(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]*Collection, len(rows))
	for i, row := range rows {
		out[i] = pgToCollection(row)
	}
	return out, nil
}

func (r *postgresRepo) GetCollection(ctx context.Context, id string) (*Collection, bool, error) {
	row, err := r.q.GetCollection(ctx, id)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	return pgToCollection(row), true, nil
}

func (r *postgresRepo) InsertCollection(ctx context.Context, c *Collection) error {
	return r.q.InsertCollection(ctx, db.InsertCollectionParams{
		ID: c.ID, Name: c.Name, Description: c.Description,
		SourceIds: encodeIDs(c.SourceIDs), AddedAt: c.AddedAt,
	})
}

func (r *postgresRepo) UpdateCollection(ctx context.Context, c *Collection) error {
	return r.q.UpdateCollection(ctx, db.UpdateCollectionParams{
		Name: c.Name, Description: c.Description, SourceIds: encodeIDs(c.SourceIDs), ID: c.ID,
	})
}

func (r *postgresRepo) DeleteCollection(ctx context.Context, id string) error {
	return r.q.DeleteCollection(ctx, id)
}

/* ---- configs ---- */

func (r *postgresRepo) ListConfigs(ctx context.Context) ([]*Config, error) {
	rows, err := r.q.ListConfigs(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]*Config, len(rows))
	for i, row := range rows {
		out[i] = pgToConfig(row)
	}
	return out, nil
}

func (r *postgresRepo) GetConfig(ctx context.Context, id string) (*Config, bool, error) {
	row, err := r.q.GetConfig(ctx, id)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	return pgToConfig(row), true, nil
}

func (r *postgresRepo) InsertConfig(ctx context.Context, c *Config) error {
	return r.q.InsertConfig(ctx, db.InsertConfigParams{
		ID: c.ID, Name: c.Name, ConfigJson: c.ConfigJSON, AddedAt: c.AddedAt,
	})
}

func (r *postgresRepo) UpdateConfig(ctx context.Context, c *Config) error {
	return r.q.UpdateConfig(ctx, db.UpdateConfigParams{Name: c.Name, ConfigJson: c.ConfigJSON, ID: c.ID})
}

func (r *postgresRepo) DeleteConfig(ctx context.Context, id string) error {
	return r.q.DeleteConfig(ctx, id)
}

/* ---- state ---- */

func (r *postgresRepo) GetState(ctx context.Context) (State, error) {
	row, err := r.q.GetState(ctx)
	if errors.Is(err, sql.ErrNoRows) {
		return State{}, nil
	}
	if err != nil {
		return State{}, err
	}
	return State{LastPoll: row.LastPoll, LastAdded: int(row.LastAdded)}, nil
}

func (r *postgresRepo) SetState(ctx context.Context, s State) error {
	return r.q.SetState(ctx, db.SetStateParams{LastPoll: s.LastPoll, LastAdded: int64(s.LastAdded)})
}
