package render

import (
	"bytes"
	"context"
	"fmt"
	"html"
	"path/filepath"
	"strings"
	"time"

	"github.com/mwyvr/firehose"
)

// healthRow is one feed's line on firehose.html.
type healthRow struct {
	Title        string
	URL          string
	Categories   string // pre-joined, subtle section reminder
	Status       string
	FailCount    int
	LastFetched  timeCell
	LastSuccess  timeCell
	NextEarliest timeCell
	Erroring     bool
	Quiet        bool
	Moved        bool
}

type healthView struct {
	SiteTitle string
	ThemeAttr string
	Version   string
	Nav       []navLink
	Feeds     []healthRow
}

// quietAfter marks a feed quiet when it has fetched fine but produced nothing
// new for this long.
const quietAfter = 7 * 24 * time.Hour

// renderHealth renders the unlinked firehose.html from feed state.
func (r *Renderer) renderHealth(ctx context.Context, out *firehose.Output, nav []navLink) error {
	stored, _, err := r.feeds.FindFeeds(ctx, firehose.FeedFilter{})
	if err != nil {
		return err
	}
	configured := map[string]bool{}
	for _, fc := range r.cfg.Feeds {
		configured[fc.URL] = true
	}

	now := r.Now()
	hv := healthView{SiteTitle: siteTitle, ThemeAttr: r.themeAttr(), Version: firehose.Version, Nav: nav}
	for _, f := range stored {
		row := healthRow{
			Title:        f.Title,
			URL:          f.URL,
			Categories:   strings.Join(f.Categories, " · "),
			Status:       f.LastStatus,
			FailCount:    f.FailCount,
			LastFetched:  cellMaybe(f.LastFetched, r.cfg.Location),
			LastSuccess:  cellMaybe(f.LastSuccess, r.cfg.Location),
			NextEarliest: cellMaybe(f.NextEarliest, r.cfg.Location),
			Erroring:     f.LastStatus != "",
			Moved:        !configured[f.URL], // stored URL diverged via 301
		}
		if row.Title == "" {
			row.Title = f.URL
		}
		if !row.Erroring && !f.LastSuccess.IsZero() && now.Sub(f.LastSuccess) > quietAfter {
			row.Quiet = true
		}
		hv.Feeds = append(hv.Feeds, row)
	}

	var buf bytes.Buffer
	if err := r.health.Execute(&buf, hv); err != nil {
		return firehose.Errorf(firehose.EINTERNAL, "render: %s: %v", out.File, err)
	}
	return atomicWrite(filepath.Join(r.cfg.Settings.OutputDir, out.File), buf.Bytes())
}

// timeCell renders an operational timestamp as a <time datetime> so the
// client-side humanizer ("3 min ago") can act on the health page too. Zero
// times render a plain em dash.
type timeCell struct {
	Display string
	ISO     string // empty for "—"
}

func cellMaybe(t time.Time, loc *time.Location) timeCell {
	if t.IsZero() {
		return timeCell{Display: "—"}
	}
	return timeCell{
		Display: t.In(loc).Format(timeDisplay),
		ISO:     t.UTC().Format(time.RFC3339),
	}
}

// WriteFallbackHealth writes a minimal failure page using only Sprintf so it
// works even when the templating layer is what broke. Used by the top-level panic
// handler.
func WriteFallbackHealth(outputDir, msg, stack string) error {
	body := fmt.Sprintf(`<!DOCTYPE html>
<html lang="en"><head><meta charset="utf-8"><title>firehose :: run failed</title></head>
<body style="font-family: system-ui, sans-serif; max-width: 68ch; margin: 2rem auto;">
<h1>firehose run failed</h1>
<p>The last generation run did not complete. River pages may be stale.</p>
<pre style="white-space: pre-wrap; background: #f4f4ef; padding: 1em;">%s

%s</pre>
</body></html>
`, html.EscapeString(msg), html.EscapeString(stack))
	return atomicWrite(filepath.Join(outputDir, "firehose.html"), []byte(body))
}
