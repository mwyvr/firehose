package sqlite

import (
	"context"
	"database/sql"
	"strings"
	"time"

	"github.com/mwyvr/firehose"
	"github.com/mwyvr/kid"
)

// ItemService is the SQLite-backed firehose.ItemService.
type ItemService struct {
	db *DB
}

// NewItemService constructs an ItemService over db.
func NewItemService(db *DB) *ItemService { return &ItemService{db: db} }

var _ firehose.ItemService = (*ItemService)(nil)

// FindItems returns items matching filter, newest-first (published desc, then
// id desc as a stable tiebreaker for deterministic output), and the total
// count before offset/limit.
//
// Category selection joins feed.categories. Because categories are stored as a
// newline-joined TEXT column, category matching is done in Go after scanning;
// the display window is applied in SQL (it is the selective predicate).
func (s *ItemService) FindItems(ctx context.Context, filter firehose.ItemFilter) ([]*firehose.Item, int, error) {
	var (
		where []string
		args  []any
	)
	if !filter.Since.IsZero() {
		where = append(where, "i.published >= ?")
		args = append(args, filter.Since.UTC())
	}

	q := `SELECT i.id, i.feed_id, i.guid, i.title, i.url, i.author,
	             i.published, i.body_html, i.summary_html, i.lead_image,
	             i.full_content, i.word_count, i.fetched_at,
	             f.categories
	      FROM item i
	      JOIN feed f ON f.id = i.feed_id`
	if len(where) > 0 {
		q += " WHERE " + strings.Join(where, " AND ")
	}
	// Deterministic ordering: newest first, id as tiebreaker.
	q += " ORDER BY i.published DESC, i.id DESC"

	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, 0, firehose.Errorf(firehose.EINTERNAL, "sqlite: find items: %v", err)
	}
	// for golangci-lint
	defer func() { _ = rows.Close() }()

	wantCats := filter.Categories // empty => ALL river (match everything)
	var items []*firehose.Item
	for rows.Next() {
		it, cats, err := s.scanItem(rows)
		if err != nil {
			return nil, 0, err
		}
		if len(wantCats) > 0 && !firehose.CategoriesIntersect(cats, wantCats) {
			continue
		}
		it.Categories = cats
		items = append(items, it)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, firehose.Errorf(firehose.EINTERNAL, "sqlite: iterate items: %v", err)
	}

	total := len(items)
	items = page(items, filter.Offset, filter.Limit)
	return items, total, nil
}

// UpsertItems inserts new items and updates changed ones, keyed by
// (feed_id, guid). Existing rows keep their kid.ID so IDs stay stable across
// runs (id.Time() and deterministic ordering both depend on this). Items with
// a zero ID are assigned a fresh kid.ID on insert.
func (s *ItemService) UpsertItems(ctx context.Context, items []*firehose.Item) error {
	if len(items) == 0 {
		return nil
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return firehose.Errorf(firehose.EINTERNAL, "sqlite: begin upsert: %v", err)
	}
	defer func() { _ = tx.Rollback() }()

	// Look up an existing id for a (feed_id, guid) so we preserve it.
	findStmt, err := tx.PrepareContext(ctx,
		`SELECT id FROM item WHERE feed_id = ? AND guid = ?`)
	if err != nil {
		return firehose.Errorf(firehose.EINTERNAL, "sqlite: prepare find: %v", err)
	}
	defer func() { _ = findStmt.Close() }()

	// ON CONFLICT updates content columns but never id or published:
	// identity and first-seen publish time are stable across re-fetches.
	upStmt, err := tx.PrepareContext(ctx, `
		INSERT INTO item
		    (id, feed_id, guid, title, url, author, published, body_html,
		     summary_html, lead_image, full_content, word_count, fetched_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(feed_id, guid) DO UPDATE SET
		    title        = excluded.title,
		    url          = excluded.url,
		    author       = excluded.author,
		    body_html    = excluded.body_html,
		    summary_html = excluded.summary_html,
		    lead_image   = excluded.lead_image,
		    full_content = excluded.full_content,
		    word_count   = excluded.word_count,
		    fetched_at   = excluded.fetched_at`)
	if err != nil {
		return firehose.Errorf(firehose.EINTERNAL, "sqlite: prepare upsert: %v", err)
	}
	defer func() { _ = upStmt.Close() }()

	for _, it := range items {
		id := it.ID
		if id.IsNil() {
			// Reuse an existing id for this (feed_id, guid) if present, else mint.
			var existing string
			err := findStmt.QueryRowContext(ctx, it.FeedID, it.GUID).Scan(&existing)
			switch err {
			case nil:
				parsed, perr := kid.FromString(existing)
				if perr != nil {
					return firehose.Errorf(firehose.EINTERNAL,
						"sqlite: bad stored id %q: %v", existing, perr)
				}
				id = parsed
			case sql.ErrNoRows:
				id = kid.New()
			default:
				return firehose.Errorf(firehose.EINTERNAL, "sqlite: lookup item id: %v", err)
			}
		}

		fullContent := 0
		if it.FullContent {
			fullContent = 1
		}

		if _, err := upStmt.ExecContext(ctx,
			id.String(), it.FeedID, it.GUID, it.Title, it.URL, it.Author,
			it.Published.UTC(), it.BodyHTML, it.SummaryHTML, it.LeadImage,
			fullContent, it.WordCount, it.FetchedAt.UTC(),
		); err != nil {
			return firehose.Errorf(firehose.EINTERNAL, "sqlite: upsert item %s: %v", it.GUID, err)
		}
		it.ID = id // reflect the resolved id back to the caller
	}

	if err := tx.Commit(); err != nil {
		return firehose.Errorf(firehose.EINTERNAL, "sqlite: commit upsert: %v", err)
	}
	return nil
}

// PurgeExpired deletes items published before olderThan (the cache-retention
// cutoff, distinct from and older than the display window). Returns the count
// removed.
func (s *ItemService) PurgeExpired(ctx context.Context, olderThan time.Time) (int, error) {
	res, err := s.db.ExecContext(ctx,
		`DELETE FROM item WHERE published < ?`, olderThan.UTC())
	if err != nil {
		return 0, firehose.Errorf(firehose.EINTERNAL, "sqlite: purge: %v", err)
	}
	n, _ := res.RowsAffected()
	return int(n), nil
}

// scanItem reads one item row plus its feed's categories column.
func (s *ItemService) scanItem(rows *sql.Rows) (*firehose.Item, []string, error) {
	var (
		it          firehose.Item
		idStr       string
		published   time.Time
		fetchedAt   time.Time
		fullContent int
		cats        string
	)
	if err := rows.Scan(
		&idStr, &it.FeedID, &it.GUID, &it.Title, &it.URL, &it.Author,
		&published, &it.BodyHTML, &it.SummaryHTML, &it.LeadImage,
		&fullContent, &it.WordCount, &fetchedAt,
		&cats,
	); err != nil {
		return nil, nil, firehose.Errorf(firehose.EINTERNAL, "sqlite: scan item: %v", err)
	}
	id, err := kid.FromString(idStr)
	if err != nil {
		return nil, nil, firehose.Errorf(firehose.EINTERNAL, "sqlite: bad item id %q: %v", idStr, err)
	}
	it.ID = id
	it.Published = published.In(s.db.loc)
	it.FetchedAt = fetchedAt.In(s.db.loc)
	it.FullContent = fullContent != 0
	return &it, decodeCategories(cats), nil
}

// ItemStats implements firehose.ItemService.
func (s *ItemService) ItemStats(ctx context.Context) ([]firehose.ItemStat, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT feed_id, published FROM item`)
	if err != nil {
		return nil, firehose.Errorf(firehose.EINTERNAL, "sqlite: item stats: %v", err)
	}
	defer func() { _ = rows.Close() }()
	var stats []firehose.ItemStat
	for rows.Next() {
		var st firehose.ItemStat
		if err := rows.Scan(&st.FeedID, &st.Published); err != nil {
			return nil, firehose.Errorf(firehose.EINTERNAL, "sqlite: item stats scan: %v", err)
		}
		stats = append(stats, st)
	}
	if err := rows.Err(); err != nil {
		return nil, firehose.Errorf(firehose.EINTERNAL, "sqlite: item stats rows: %v", err)
	}
	return stats, nil
}
