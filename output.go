package firehose

import (
	"slices"
	"time"
)

// BodyScope is a rendering scope for item bodies, resolved three-tier
// (settings -> output -> feed).
type BodyScope string

const (
	BodyTitle         BodyScope = "title"          // headline only
	BodyExcerpt       BodyScope = "excerpt"        // excerpt + title-link
	BodyFull          BodyScope = "full"           // full feed body, always expanded
	BodyExcerptExpand BodyScope = "excerpt+expand" // excerpt + collapsed full body (<details>)
)

// ExcerptImage controls whether the lead image survives into an excerpt.
type ExcerptImage string

const (
	ExcerptImageLead ExcerptImage = "lead" // keep first image, lazy, capped height
	ExcerptImageNone ExcerptImage = "none" // drop images from the excerpt
)

// Output is one rendered river page (a section, or the ALL river, or the
// unlinked health page). Sections are not a special HTML type — an Output is
// the same template rendered against a different ItemFilter. The only
// section-aware element on a page is the nav strip, which is data-driven from
// the set of Outputs.
type Output struct {
	Name  string // stable identifier ("gov", "all")
	File  string // output filename ("gov.html", "index.html")
	Title string // page <h1> ("Government & EM")

	// Categories selects which items appear. ["*"] (or empty) is the ALL river.
	Categories []string

	// Per-output overrides (win over settings, lose to per-feed).
	Body         BodyScope
	ExcerptImage ExcerptImage
	ReadingTime  *bool

	// InNav controls whether this output appears in the cross-page nav strip.
	// The firehose.html health page sets this false — generated every run, but
	// unlinked to the "public" nav. There is no security in this scheme.
	InNav bool

	// Health marks the special firehose.html output, rendered from feed
	// health state rather than the item river.
	Health bool
}

// IsAll reports whether this output is the ALL river (empty or "*" categories).
func (o *Output) IsAll() bool {
	if len(o.Categories) == 0 {
		return true
	}
	return slices.Contains(o.Categories, "*")
}

// Filter builds the ItemFilter for this output given the display-window cutoff
// (items published before since are excluded).
func (o *Output) Filter(since time.Time) ItemFilter {
	f := ItemFilter{Since: since}
	if !o.IsAll() {
		f.Categories = o.Categories
	}
	return f
}
