-- firehose cache schema. The cache exists for dedupe, conditional-GET
-- validators, retention, and feed health — NOT read state.
-- It is disposable by design.

CREATE TABLE feed (
    id             INTEGER PRIMARY KEY,    -- rowid join key; identity is url.
    url            TEXT NOT NULL UNIQUE,   -- canonical; updated in place on 301
    title          TEXT NOT NULL DEFAULT '',
    categories     TEXT NOT NULL DEFAULT '',  -- newline-joined; feeds may have several
    etag           TEXT NOT NULL DEFAULT '',
    last_modified  TEXT NOT NULL DEFAULT '',
    fail_count     INTEGER NOT NULL DEFAULT 0,
    last_status    TEXT NOT NULL DEFAULT '',   -- '' | not_found | parse | timeout | panic | ...
    last_success   TIMESTAMP,                  -- last fetch that produced items
    last_fetched   TIMESTAMP,
    next_earliest  TIMESTAMP                   -- backoff gate
);

CREATE TABLE item (
    id           TEXT PRIMARY KEY,          -- kid.ID
    feed_id      INTEGER NOT NULL REFERENCES feed(id) ON DELETE CASCADE,
    guid         TEXT NOT NULL,             -- feed-provided; dedupe with feed_id
    title        TEXT NOT NULL DEFAULT '',
    url          TEXT NOT NULL DEFAULT '',  -- may be empty (linkless-title rule)
    author       TEXT NOT NULL DEFAULT '',
    published    TIMESTAMP NOT NULL,        -- sort key for the river
    body_html    TEXT NOT NULL DEFAULT '',  -- already sanitized (+ declared-lang highlight)
    summary_html TEXT NOT NULL DEFAULT '',  -- sanitized feed summary, when distinct from body
    lead_image   TEXT NOT NULL DEFAULT '',  -- first image URL (inline or media/enclosure)
    full_content INTEGER NOT NULL DEFAULT 0,-- 1 if feed shipped full body
    word_count   INTEGER NOT NULL DEFAULT 0,
    fetched_at   TIMESTAMP NOT NULL,
    UNIQUE(feed_id, guid)
);

CREATE INDEX item_published_idx ON item(published);
CREATE INDEX item_feed_idx ON item(feed_id);
