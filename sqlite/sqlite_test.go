package sqlite_test

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/mwyvr/firehose"
	"github.com/mwyvr/firehose/sqlite"
)

// newTestDB opens an in-memory database. Requires the real modernc.org/sqlite
// driver (blank-imported by the sqlite package), so it runs under `go test`
// locally where modules are fetchable.
func newTestDB(t *testing.T) *sqlite.DB {
	t.Helper()
	// Open pins a single connection (max/idle 1, no idle timeout), so a
	// ":memory:" database survives for the lifetime of the DB rather than being
	// discarded when database/sql retires an idle connection.
	db := sqlite.NewDB(":memory:", time.UTC)
	if err := db.Open(context.Background()); err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

func TestMigrateIdempotent(t *testing.T) {
	// A :memory: reopen is a fresh database and would test nothing; a file DB
	// forces the second Open to re-run migrate over the existing schema, which
	// must be skipped via the schema_migrations ledger rather than erroring on
	// duplicate tables.
	path := filepath.Join(t.TempDir(), "cache.db")

	db1 := sqlite.NewDB(path, time.UTC)
	if err := db1.Open(context.Background()); err != nil {
		t.Fatalf("first open: %v", err)
	}
	if err := db1.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}

	db2 := sqlite.NewDB(path, time.UTC)
	if err := db2.Open(context.Background()); err != nil {
		t.Fatalf("second open (re-migrate) failed: %v", err)
	}
	_ = db2.Close()
}

func TestFeedSyncInsertUpdateDelete(t *testing.T) {
	ctx := context.Background()
	db := newTestDB(t)
	fs := sqlite.NewFeedService(db)

	// Insert two feeds.
	err := fs.SyncFeeds(ctx, []*firehose.Feed{
		{URL: "https://a.example/feed", Title: "A", Categories: []string{"gov"}},
		{URL: "https://b.example/feed", Title: "B", Categories: []string{"tech", "ai"}},
	})
	if err != nil {
		t.Fatalf("sync insert: %v", err)
	}

	feeds, total, err := fs.FindFeeds(ctx, firehose.FeedFilter{})
	if err != nil {
		t.Fatalf("find: %v", err)
	}
	if total != 2 {
		t.Fatalf("want 2 feeds, got %d", total)
	}

	// Give feed A fetch state, then re-sync: state must be preserved, title
	// updated, feed B removed, feed C added.
	if _, err := fs.UpdateFeed(ctx, feeds[0].ID, firehose.FeedUpdate{
		ETag: strptr(`"abc"`), FailCount: intptr(3),
	}); err != nil {
		t.Fatalf("update: %v", err)
	}

	err = fs.SyncFeeds(ctx, []*firehose.Feed{
		{URL: "https://a.example/feed", Title: "A2", Categories: []string{"gov", "em"}},
		{URL: "https://c.example/feed", Title: "C", Categories: []string{"food"}},
	})
	if err != nil {
		t.Fatalf("sync reconcile: %v", err)
	}

	a, err := fs.FindFeedByURL(ctx, "https://a.example/feed")
	if err != nil {
		t.Fatalf("find A: %v", err)
	}
	if a.Title != "A2" {
		t.Errorf("title not updated: %q", a.Title)
	}
	if a.ETag != `"abc"` || a.FailCount != 3 {
		t.Errorf("fetch state not preserved across sync: etag=%q fail=%d", a.ETag, a.FailCount)
	}
	if len(a.Categories) != 2 {
		t.Errorf("categories not updated: %v", a.Categories)
	}

	if _, err := fs.FindFeedByURL(ctx, "https://b.example/feed"); firehose.ErrorCode(err) != firehose.ENOTFOUND {
		t.Errorf("feed B should be deleted, got err=%v", err)
	}
}

func TestItemUpsertPreservesID(t *testing.T) {
	ctx := context.Background()
	db := newTestDB(t)
	fs := sqlite.NewFeedService(db)
	is := sqlite.NewItemService(db)

	if err := fs.SyncFeeds(ctx, []*firehose.Feed{
		{URL: "https://a.example/feed", Categories: []string{"gov"}},
	}); err != nil {
		t.Fatalf("sync: %v", err)
	}
	feed, _ := fs.FindFeedByURL(ctx, "https://a.example/feed")

	now := time.Now().UTC().Truncate(time.Second)
	item := &firehose.Item{
		FeedID: feed.ID, GUID: "guid-1", Title: "First",
		Published: now, FetchedAt: now, FullContent: true, WordCount: 100,
	}
	if err := is.UpsertItems(ctx, []*firehose.Item{item}); err != nil {
		t.Fatalf("insert: %v", err)
	}
	firstID := item.ID
	if firstID.IsNil() {
		t.Fatal("id not assigned on insert")
	}

	// Re-publish same (feed, guid) with a changed title and no preset id.
	update := &firehose.Item{
		FeedID: feed.ID, GUID: "guid-1", Title: "First (edited)",
		Published: now, FetchedAt: now.Add(time.Hour), WordCount: 120,
	}
	if err := is.UpsertItems(ctx, []*firehose.Item{update}); err != nil {
		t.Fatalf("update: %v", err)
	}
	if update.ID != firstID {
		t.Errorf("id not preserved on re-publish: was %s now %s", firstID, update.ID)
	}

	items, total, err := is.FindItems(ctx, firehose.ItemFilter{})
	if err != nil {
		t.Fatalf("find: %v", err)
	}
	if total != 1 {
		t.Fatalf("re-publish created a duplicate: %d items", total)
	}
	if items[0].Title != "First (edited)" {
		t.Errorf("content not updated: %q", items[0].Title)
	}
}

func TestItemCategoryFilterAndAllRiver(t *testing.T) {
	ctx := context.Background()
	db := newTestDB(t)
	fs := sqlite.NewFeedService(db)
	is := sqlite.NewItemService(db)

	_ = fs.SyncFeeds(ctx, []*firehose.Feed{
		{URL: "https://gov.example/f", Categories: []string{"gov", "em"}},
		{URL: "https://tech.example/f", Categories: []string{"tech"}},
	})
	gov, _ := fs.FindFeedByURL(ctx, "https://gov.example/f")
	tech, _ := fs.FindFeedByURL(ctx, "https://tech.example/f")

	now := time.Now().UTC()
	_ = is.UpsertItems(ctx, []*firehose.Item{
		{FeedID: gov.ID, GUID: "g1", Title: "gov item", Published: now, FetchedAt: now},
		{FeedID: tech.ID, GUID: "t1", Title: "tech item", Published: now.Add(-time.Minute), FetchedAt: now},
	})

	// ALL river (empty categories) sees both.
	all, total, _ := is.FindItems(ctx, firehose.ItemFilter{})
	if total != 2 {
		t.Fatalf("ALL river: want 2, got %d", total)
	}
	// Deterministic order: newest first.
	if all[0].Title != "gov item" {
		t.Errorf("river not newest-first: %q first", all[0].Title)
	}

	// gov section (matches gov OR em) sees only the gov item.
	govItems, gTotal, _ := is.FindItems(ctx, firehose.ItemFilter{Categories: []string{"gov", "em"}})
	if gTotal != 1 || govItems[0].Title != "gov item" {
		t.Errorf("gov section wrong: total=%d items=%v", gTotal, govItems)
	}
}

func TestPurgeExpired(t *testing.T) {
	ctx := context.Background()
	db := newTestDB(t)
	fs := sqlite.NewFeedService(db)
	is := sqlite.NewItemService(db)

	_ = fs.SyncFeeds(ctx, []*firehose.Feed{{URL: "https://a.example/f", Categories: []string{"x"}}})
	feed, _ := fs.FindFeedByURL(ctx, "https://a.example/f")

	now := time.Now().UTC()
	old := now.Add(-40 * 24 * time.Hour)
	_ = is.UpsertItems(ctx, []*firehose.Item{
		{FeedID: feed.ID, GUID: "fresh", Published: now, FetchedAt: now},
		{FeedID: feed.ID, GUID: "stale", Published: old, FetchedAt: old},
	})

	cutoff := now.Add(-30 * 24 * time.Hour)
	n, err := is.PurgeExpired(ctx, cutoff)
	if err != nil {
		t.Fatalf("purge: %v", err)
	}
	if n != 1 {
		t.Errorf("want 1 purged, got %d", n)
	}
	_, total, _ := is.FindItems(ctx, firehose.ItemFilter{})
	if total != 1 {
		t.Errorf("want 1 remaining, got %d", total)
	}
}

func strptr(s string) *string { return &s }
func intptr(i int) *int       { return &i }

// TestSyncFeedsFlipFlop pins config-URL reconciliation: swapping a feed URL
// and swapping it back must converge the cache to exactly the configured
// URL, with the abandoned URL (and its items, via cascade) gone. Exercised
// by a real user flip-flopping news.ycombinator.com <-> hnrss.org.
func TestSyncFeedsFlipFlop(t *testing.T) {
	db := newTestDB(t)
	fs := sqlite.NewFeedService(db)
	ctx := context.Background()

	sync := func(url string) {
		t.Helper()
		if err := fs.SyncFeeds(ctx, []*firehose.Feed{{URL: url, Title: "HN", Categories: []string{"tech"}}}); err != nil {
			t.Fatalf("sync %s: %v", url, err)
		}
	}
	urls := func() []string {
		t.Helper()
		got, _, err := fs.FindFeeds(ctx, firehose.FeedFilter{})
		if err != nil {
			t.Fatal(err)
		}
		var out []string
		for _, f := range got {
			out = append(out, f.URL)
		}
		return out
	}

	orig := "https://news.ycombinator.com/rss"
	alt := "https://hnrss.org/frontpage"

	sync(orig)
	sync(alt)  // user swaps
	sync(orig) // user swaps back

	got := urls()
	if len(got) != 1 || got[0] != orig {
		t.Fatalf("after flip-flop want exactly [%s], got %v", orig, got)
	}
}
