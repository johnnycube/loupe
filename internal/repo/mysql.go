package repo

import (
	"context"
	"database/sql"
	"errors"

	db "github.com/johnnycube/loupe/internal/db/mysql"
)

// mysqlRepo adapts the generated mysql.Queries to the Repo interface. It is
// line-for-line identical to sqliteRepo/postgresRepo except for the imported
// generated package — the LCD schema guarantees matching signatures.
type mysqlRepo struct {
	db *sql.DB
	q  *db.Queries
}

func newMySQLRepo(sqlDB *sql.DB) (Repo, error) {
	if err := runMigrations(sqlDB, db.Migrations, "mysql"); err != nil {
		return nil, err
	}
	return &mysqlRepo{db: sqlDB, q: db.New(sqlDB)}, nil
}

func (r *mysqlRepo) Close() error { return r.db.Close() }

func (r *mysqlRepo) WithinTx(ctx context.Context, fn func(Repo) error) error {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	if err := fn(&mysqlRepo{db: r.db, q: r.q.WithTx(tx)}); err != nil {
		_ = tx.Rollback()
		return err
	}
	return tx.Commit()
}

/* ---- mappers ---- */

func myToItem(it db.Item) *Item {
	return &Item{
		ID: it.ID, SourceID: it.SourceID, Label: it.Label, ImageKey: it.ImageKey,
		URL: it.Url, Sample: it.Sample, Title: it.Title, Status: it.Status,
		Gone: i2b(it.Gone), AddedAt: it.AddedAt, LastSeen: it.LastSeen, DecidedAt: it.DecidedAt,
	}
}

func myToSource(s db.Source) *Source {
	return &Source{
		ID: s.ID, Name: s.Name, Description: s.Description, URL: s.Url,
		ConfigFile: s.ConfigFile, ConfigJSON: s.ConfigJson, ConfigID: s.ConfigID, AddedAt: s.AddedAt,
		LastPoll: s.LastPoll, LastAdded: int(s.LastAdded), LastError: s.LastError,
	}
}

func myToConfig(c db.Config) *Config {
	return &Config{ID: c.ID, Name: c.Name, ConfigJSON: c.ConfigJson, AddedAt: c.AddedAt}
}

func myToCollection(c db.Collection) *Collection {
	return &Collection{
		ID: c.ID, Name: c.Name, Description: c.Description,
		SourceIDs: decodeIDs(c.SourceIds), AddedAt: c.AddedAt,
	}
}

/* ---- items ---- */

func (r *mysqlRepo) ListReviewable(ctx context.Context) ([]*Item, error) {
	rows, err := r.q.ListReviewable(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]*Item, len(rows))
	for i, row := range rows {
		out[i] = myToItem(row)
	}
	return out, nil
}

func (r *mysqlRepo) ListStaleNew(ctx context.Context) ([]*Item, error) {
	rows, err := r.q.ListStaleNew(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]*Item, len(rows))
	for i, row := range rows {
		out[i] = myToItem(row)
	}
	return out, nil
}

func (r *mysqlRepo) ListGood(ctx context.Context) ([]*Item, error) {
	rows, err := r.q.ListGood(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]*Item, len(rows))
	for i, row := range rows {
		out[i] = myToItem(row)
	}
	return out, nil
}

func (r *mysqlRepo) GetItem(ctx context.Context, id string) (*Item, bool, error) {
	row, err := r.q.GetItem(ctx, id)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	return myToItem(row), true, nil
}

func (r *mysqlRepo) InsertItemIfNew(ctx context.Context, it *Item) (bool, error) {
	n, err := r.q.InsertItemIfNew(ctx, db.InsertItemIfNewParams{
		ID: it.ID, SourceID: it.SourceID, Label: it.Label, ImageKey: it.ImageKey,
		Url: it.URL, Sample: it.Sample, Title: it.Title, Status: it.Status,
		Gone: b2i(it.Gone), AddedAt: it.AddedAt, LastSeen: it.LastSeen, DecidedAt: it.DecidedAt,
	})
	return n > 0, err
}

func (r *mysqlRepo) UnstaleItem(ctx context.Context, id string, lastSeen int64) error {
	_, err := r.q.UnstaleItem(ctx, db.UnstaleItemParams{LastSeen: lastSeen, ID: id})
	return err
}

func (r *mysqlRepo) DecideItem(ctx context.Context, id, status string, decidedAt int64) (bool, error) {
	n, err := r.q.DecideItem(ctx, db.DecideItemParams{Status: status, DecidedAt: decidedAt, ID: id})
	return n > 0, err
}

func (r *mysqlRepo) SetItemStatus(ctx context.Context, id, status string) (bool, error) {
	n, err := r.q.SetItemStatus(ctx, db.SetItemStatusParams{Status: status, ID: id})
	return n > 0, err
}

func (r *mysqlRepo) ResetItem(ctx context.Context, id string) (bool, error) {
	n, err := r.q.ResetItem(ctx, id)
	return n > 0, err
}

func (r *mysqlRepo) MarkSourceGone(ctx context.Context, sourceID string) error {
	return r.q.MarkSourceGone(ctx, sourceID)
}

func (r *mysqlRepo) PurgeStale(ctx context.Context, sourceID string) (int, error) {
	n, err := r.q.PurgeStale(ctx, sourceID)
	return int(n), err
}

func (r *mysqlRepo) DeleteNewBySource(ctx context.Context, sourceID string) error {
	return r.q.DeleteNewBySource(ctx, sourceID)
}

func (r *mysqlRepo) CountByStatusGone(ctx context.Context) ([]StatusGoneCount, error) {
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

func (r *mysqlRepo) CountBySourceStatusGone(ctx context.Context) ([]SourceStatusGoneCount, error) {
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

func (r *mysqlRepo) ListSources(ctx context.Context) ([]*Source, error) {
	rows, err := r.q.ListSources(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]*Source, len(rows))
	for i, row := range rows {
		out[i] = myToSource(row)
	}
	return out, nil
}

func (r *mysqlRepo) GetSource(ctx context.Context, id string) (*Source, bool, error) {
	row, err := r.q.GetSource(ctx, id)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	return myToSource(row), true, nil
}

func (r *mysqlRepo) InsertSource(ctx context.Context, s *Source) error {
	return r.q.InsertSource(ctx, db.InsertSourceParams{
		ID: s.ID, Name: s.Name, Description: s.Description, Url: s.URL,
		ConfigFile: s.ConfigFile, ConfigJson: s.ConfigJSON, ConfigID: s.ConfigID, AddedAt: s.AddedAt,
		LastPoll: s.LastPoll, LastAdded: int64(s.LastAdded), LastError: s.LastError,
	})
}

func (r *mysqlRepo) UpdateSource(ctx context.Context, s *Source) error {
	return r.q.UpdateSource(ctx, db.UpdateSourceParams{
		Name: s.Name, Description: s.Description, Url: s.URL, ConfigFile: s.ConfigFile,
		ConfigJson: s.ConfigJSON, ConfigID: s.ConfigID, LastPoll: s.LastPoll, LastAdded: int64(s.LastAdded),
		LastError: s.LastError, ID: s.ID,
	})
}

func (r *mysqlRepo) MarkPolled(ctx context.Context, id string, lastPoll int64, lastAdded int) error {
	return r.q.MarkPolled(ctx, db.MarkPolledParams{LastPoll: lastPoll, LastAdded: int64(lastAdded), ID: id})
}

func (r *mysqlRepo) SetSourceError(ctx context.Context, id, msg string, lastPoll int64) error {
	return r.q.SetSourceError(ctx, db.SetSourceErrorParams{LastError: msg, LastPoll: lastPoll, ID: id})
}

func (r *mysqlRepo) DeleteSource(ctx context.Context, id string) error {
	return r.q.DeleteSource(ctx, id)
}

/* ---- collections ---- */

func (r *mysqlRepo) ListCollections(ctx context.Context) ([]*Collection, error) {
	rows, err := r.q.ListCollections(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]*Collection, len(rows))
	for i, row := range rows {
		out[i] = myToCollection(row)
	}
	return out, nil
}

func (r *mysqlRepo) GetCollection(ctx context.Context, id string) (*Collection, bool, error) {
	row, err := r.q.GetCollection(ctx, id)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	return myToCollection(row), true, nil
}

func (r *mysqlRepo) InsertCollection(ctx context.Context, c *Collection) error {
	return r.q.InsertCollection(ctx, db.InsertCollectionParams{
		ID: c.ID, Name: c.Name, Description: c.Description,
		SourceIds: encodeIDs(c.SourceIDs), AddedAt: c.AddedAt,
	})
}

func (r *mysqlRepo) UpdateCollection(ctx context.Context, c *Collection) error {
	return r.q.UpdateCollection(ctx, db.UpdateCollectionParams{
		Name: c.Name, Description: c.Description, SourceIds: encodeIDs(c.SourceIDs), ID: c.ID,
	})
}

func (r *mysqlRepo) DeleteCollection(ctx context.Context, id string) error {
	return r.q.DeleteCollection(ctx, id)
}

/* ---- configs ---- */

func (r *mysqlRepo) ListConfigs(ctx context.Context) ([]*Config, error) {
	rows, err := r.q.ListConfigs(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]*Config, len(rows))
	for i, row := range rows {
		out[i] = myToConfig(row)
	}
	return out, nil
}

func (r *mysqlRepo) GetConfig(ctx context.Context, id string) (*Config, bool, error) {
	row, err := r.q.GetConfig(ctx, id)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	return myToConfig(row), true, nil
}

func (r *mysqlRepo) InsertConfig(ctx context.Context, c *Config) error {
	return r.q.InsertConfig(ctx, db.InsertConfigParams{
		ID: c.ID, Name: c.Name, ConfigJson: c.ConfigJSON, AddedAt: c.AddedAt,
	})
}

func (r *mysqlRepo) UpdateConfig(ctx context.Context, c *Config) error {
	return r.q.UpdateConfig(ctx, db.UpdateConfigParams{Name: c.Name, ConfigJson: c.ConfigJSON, ID: c.ID})
}

func (r *mysqlRepo) DeleteConfig(ctx context.Context, id string) error {
	return r.q.DeleteConfig(ctx, id)
}

/* ---- state ---- */

func (r *mysqlRepo) GetState(ctx context.Context) (State, error) {
	row, err := r.q.GetState(ctx)
	if errors.Is(err, sql.ErrNoRows) {
		return State{}, nil
	}
	if err != nil {
		return State{}, err
	}
	return State{LastPoll: row.LastPoll, LastAdded: int(row.LastAdded)}, nil
}

func (r *mysqlRepo) SetState(ctx context.Context, s State) error {
	return r.q.SetState(ctx, db.SetStateParams{LastPoll: s.LastPoll, LastAdded: int64(s.LastAdded)})
}
