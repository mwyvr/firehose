package sqlite

import (
	"context"
	"database/sql"
	"strings"
	"time"

	"github.com/mwyvr/firehose"
)

// categorySep joins a feed's categories in a single TEXT column. Newline is
// safe: categories are short tokens that never contain it.
const categorySep = "\n"

// decodeCategories splits the stored newline-joined list.
func decodeCategories(s string) []string {
	if s == "" {
		return nil
	}
	return strings.Split(s, categorySep)
}

// FeedService is the SQLite-backed firehose.FeedService.
type FeedService struct {
	db *DB
}

// NewFeedService constructs a FeedService over db.
func NewFeedService(db *DB) *FeedService { return &FeedService{db: db} }

var _ firehose.FeedService = (*FeedService)(nil)

// FindFeeds returns feeds matching filter and the total count (ignoring offset/limit).
func (s *FeedService) FindFeeds(ctx context.Context, filter firehose.FeedFilter) ([]*firehose.Feed, int, error) {
	var (
		where []string
		args  []any
	)
	if filter.ID != nil {
		where = append(where, "id = ?")
		args = append(args, *filter.ID)
	}
	if filter.URL != nil {
		where = append(where, "url = ?")
		args = append(args, *filter.URL)
	}
	if filter.DueOnly {
		where = append(where, "(next_earliest IS NULL OR next_earliest <= ?)")
		args = append(args, time.Now().UTC())
	}
	// Category filter is applied in Go after decode, since it is a joined TEXT
	// column; feed counts are tiny so this is fine.

	q := `SELECT id, url, title, categories, etag, last_modified,
	             fail_count, last_status, last_success, last_fetched, next_earliest
	      FROM feed`
	if len(where) > 0 {
		q += " WHERE " + strings.Join(where, " AND ")
	}
	q += " ORDER BY id"

	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, 0, firehose.Errorf(firehose.EINTERNAL, "sqlite: find feeds: %v", err)
	}
	defer func() { _ = rows.Close() }()

	var feeds []*firehose.Feed
	for rows.Next() {
		f, err := s.scanFeed(rows)
		if err != nil {
			return nil, 0, err
		}
		if filter.Category != nil && !firehose.ContainsCategory(f.Categories, *filter.Category) {
			continue
		}
		feeds = append(feeds, f)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, firehose.Errorf(firehose.EINTERNAL, "sqlite: iterate feeds: %v", err)
	}

	total := len(feeds)
	feeds = page(feeds, filter.Offset, filter.Limit)
	return feeds, total, nil
}

// FindFeedByURL returns the feed with the given URL or an ENOTFOUND error.
func (s *FeedService) FindFeedByURL(ctx context.Context, url string) (*firehose.Feed, error) {
	feeds, _, err := s.FindFeeds(ctx, firehose.FeedFilter{URL: &url})
	if err != nil {
		return nil, err
	}
	if len(feeds) == 0 {
		return nil, firehose.Errorf(firehose.ENOTFOUND, "feed %q not found", url)
	}
	return feeds[0], nil
}

// SyncFeeds reconciles the configured feed set into the cache: inserts new
// feeds, updates categories/config on existing ones (matched by URL), and
// deletes feeds no longer configured (cascading their items). Fetch-state
// columns on existing feeds are preserved.
func (s *FeedService) SyncFeeds(ctx context.Context, feeds []*firehose.Feed) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return firehose.Errorf(firehose.EINTERNAL, "sqlite: begin sync: %v", err)
	}
	defer func() { _ = tx.Rollback() }()

	// Existing URLs currently in the cache.
	existing := map[string]bool{}
	rows, err := tx.QueryContext(ctx, `SELECT url FROM feed`)
	if err != nil {
		return firehose.Errorf(firehose.EINTERNAL, "sqlite: read existing feeds: %v", err)
	}
	for rows.Next() {
		var u string
		if err := rows.Scan(&u); err != nil {
			_ = rows.Close()
			return firehose.Errorf(firehose.EINTERNAL, "sqlite: scan existing url: %v", err)
		}
		existing[u] = true
	}
	_ = rows.Close()
	if err := rows.Err(); err != nil {
		return firehose.Errorf(firehose.EINTERNAL, "sqlite: iterate existing feeds: %v", err)
	}

	configured := map[string]bool{}
	for _, f := range feeds {
		configured[f.URL] = true
		cats := strings.Join(f.Categories, categorySep)
		if existing[f.URL] {
			// Update only config-derived columns; preserve fetch state. Title
			// is overwritten only when config provides an override — an empty
			// config title must not erase the feed's self-reported title
			// stored by the fetcher.
			if f.Title != "" {
				if _, err := tx.ExecContext(ctx,
					`UPDATE feed SET title = ?, categories = ? WHERE url = ?`,
					f.Title, cats, f.URL,
				); err != nil {
					return firehose.Errorf(firehose.EINTERNAL, "sqlite: update feed %s: %v", f.URL, err)
				}
			} else {
				if _, err := tx.ExecContext(ctx,
					`UPDATE feed SET categories = ? WHERE url = ?`,
					cats, f.URL,
				); err != nil {
					return firehose.Errorf(firehose.EINTERNAL, "sqlite: update feed %s: %v", f.URL, err)
				}
			}
		} else {
			if _, err := tx.ExecContext(ctx,
				`INSERT INTO feed (url, title, categories) VALUES (?, ?, ?)`,
				f.URL, f.Title, cats,
			); err != nil {
				return firehose.Errorf(firehose.EINTERNAL, "sqlite: insert feed %s: %v", f.URL, err)
			}
		}
	}

	// Delete feeds no longer configured; items cascade.
	for u := range existing {
		if !configured[u] {
			if _, err := tx.ExecContext(ctx, `DELETE FROM feed WHERE url = ?`, u); err != nil {
				return firehose.Errorf(firehose.EINTERNAL, "sqlite: delete feed %s: %v", u, err)
			}
		}
	}

	if err := tx.Commit(); err != nil {
		return firehose.Errorf(firehose.EINTERNAL, "sqlite: commit sync: %v", err)
	}
	return nil
}

// UpdateFeed applies fetch/health state. Nil fields in upd are left unchanged.
func (s *FeedService) UpdateFeed(ctx context.Context, id int64, upd firehose.FeedUpdate) (*firehose.Feed, error) {
	var (
		set  []string
		args []any
	)
	if upd.URL != nil {
		set = append(set, "url = ?")
		args = append(args, *upd.URL)
	}
	if upd.Title != nil {
		set = append(set, "title = ?")
		args = append(args, *upd.Title)
	}
	if upd.ETag != nil {
		set = append(set, "etag = ?")
		args = append(args, *upd.ETag)
	}
	if upd.LastModified != nil {
		set = append(set, "last_modified = ?")
		args = append(args, *upd.LastModified)
	}
	if upd.FailCount != nil {
		set = append(set, "fail_count = ?")
		args = append(args, *upd.FailCount)
	}
	if upd.LastStatus != nil {
		set = append(set, "last_status = ?")
		args = append(args, *upd.LastStatus)
	}
	if upd.LastSuccess != nil {
		set = append(set, "last_success = ?")
		args = append(args, nullTime(*upd.LastSuccess))
	}
	if upd.LastFetched != nil {
		set = append(set, "last_fetched = ?")
		args = append(args, nullTime(*upd.LastFetched))
	}
	if upd.NextEarliest != nil {
		set = append(set, "next_earliest = ?")
		args = append(args, nullTime(*upd.NextEarliest))
	}

	if len(set) == 0 {
		return s.findFeedByID(ctx, id)
	}

	args = append(args, id)
	q := "UPDATE feed SET " + strings.Join(set, ", ") + " WHERE id = ?"
	res, err := s.db.ExecContext(ctx, q, args...)
	if err != nil {
		return nil, firehose.Errorf(firehose.EINTERNAL, "sqlite: update feed %d: %v", id, err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return nil, firehose.Errorf(firehose.ENOTFOUND, "feed id %d not found", id)
	}
	return s.findFeedByID(ctx, id)
}

func (s *FeedService) findFeedByID(ctx context.Context, id int64) (*firehose.Feed, error) {
	feeds, _, err := s.FindFeeds(ctx, firehose.FeedFilter{ID: &id})
	if err != nil {
		return nil, err
	}
	if len(feeds) == 0 {
		return nil, firehose.Errorf(firehose.ENOTFOUND, "feed id %d not found", id)
	}
	return feeds[0], nil
}

// scanFeed reads one feed row.
func (s *FeedService) scanFeed(rows *sql.Rows) (*firehose.Feed, error) {
	var (
		f            firehose.Feed
		cats         string
		lastSuccess  sql.NullTime
		lastFetched  sql.NullTime
		nextEarliest sql.NullTime
	)
	if err := rows.Scan(
		&f.ID, &f.URL, &f.Title, &cats, &f.ETag, &f.LastModified,
		&f.FailCount, &f.LastStatus, &lastSuccess, &lastFetched, &nextEarliest,
	); err != nil {
		return nil, firehose.Errorf(firehose.EINTERNAL, "sqlite: scan feed: %v", err)
	}
	f.Categories = decodeCategories(cats)
	f.LastSuccess = s.db.scanTime(lastSuccess)
	f.LastFetched = s.db.scanTime(lastFetched)
	f.NextEarliest = s.db.scanTime(nextEarliest)
	return &f, nil
}
