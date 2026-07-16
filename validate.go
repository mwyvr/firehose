package firehose

import "strings"

// Validate checks the fully-defaulted config for internal consistency. Returns
// the first problem as an EINVALID error. This is the core of `firehose check`.
func (c *Config) Validate() error {
	s := c.Settings
	if s.OutputDir == "" {
		return Errorf(EINVALID, "settings.output_dir is required")
	}
	if s.CacheDB == "" {
		return Errorf(EINVALID, "settings.cache_db is required")
	}
	if s.CacheRetention.D() < s.DisplayWindow.D() {
		return Errorf(EINVALID,
			"cache_retention (%s) must be >= display_window (%s): retention shorter than the display window causes re-published old items to reappear",
			s.CacheRetention.D(), s.DisplayWindow.D())
	}
	if !validBodyScopes[s.Body] {
		return Errorf(EINVALID, "settings.body %q invalid (want title|excerpt|full|excerpt+expand)", s.Body)
	}
	if !validExcerptImages[s.ExcerptImage] {
		return Errorf(EINVALID, "settings.excerpt_image %q invalid (want lead|none)", s.ExcerptImage)
	}
	if s.ExcerptWords < 1 {
		return Errorf(EINVALID, "settings.excerpt_words must be >= 1")
	}

	if len(c.Outputs) == 0 {
		return Errorf(EINVALID, "at least one [[output]] is required")
	}

	// Collect the set of categories any output selects, and detect an ALL
	// river. Validate per-output fields and duplicate output files.
	seenFile := map[string]string{} // file -> output name
	seenName := map[string]bool{}
	selected := map[string]bool{} // categories covered by some output
	haveAll := false

	for i := range c.Outputs {
		o := &c.Outputs[i]
		if o.Name == "" {
			return Errorf(EINVALID, "output[%d]: name is required", i)
		}
		if seenName[o.Name] {
			return Errorf(EINVALID, "duplicate output name %q", o.Name)
		}
		seenName[o.Name] = true

		if o.File == "" {
			return Errorf(EINVALID, "output %q: file is required", o.Name)
		}
		if prev, dup := seenFile[o.File]; dup {
			return Errorf(EINVALID, "outputs %q and %q both write file %q", prev, o.Name, o.File)
		}
		seenFile[o.File] = o.Name

		if o.Body != "" && !validBodyScopes[o.Body] {
			return Errorf(EINVALID, "output %q: body %q invalid", o.Name, o.Body)
		}
		if o.ExcerptImage != "" && !validExcerptImages[o.ExcerptImage] {
			return Errorf(EINVALID, "output %q: excerpt_image %q invalid", o.Name, o.ExcerptImage)
		}

		if len(o.Categories) == 0 {
			haveAll = true
		}
		for _, cat := range o.Categories {
			if cat == "*" {
				haveAll = true
			} else {
				selected[strings.ToLower(cat)] = true // matching is case-insensitive
			}
		}
	}

	// Every feed category must be reachable: either an ALL river exists, or
	// some output selects that category. Otherwise items would be fetched and
	// never rendered anywhere — a silent black hole.
	switch c.Settings.Theme {
	case "auto", "light", "dark":
	default:
		return Errorf(EINVALID, "settings.theme %q: want auto, light, or dark", c.Settings.Theme)
	}

	for _, fc := range c.Feeds {
		if d := fc.DisplayWindow.D(); d > 0 && d > c.Settings.CacheRetention.D() {
			return Errorf(EINVALID,
				"feed %s: display_window %s exceeds cache_retention %s (items would be purged before they could render)",
				fc.URL, fc.DisplayWindow.D(), c.Settings.CacheRetention.D())
		}
	}

	seenFeed := map[string]bool{}
	for i := range c.Feeds {
		fd := &c.Feeds[i]
		if fd.URL == "" {
			return Errorf(EINVALID, "feed[%d]: url is required", i)
		}
		for from, to := range fd.RewriteHost {
			if from == "" || to == "" || strings.ContainsAny(from+to, "/:") {
				return Errorf(EINVALID,
					"feed %s: rewrite_host entries must map bare hostname to bare hostname, got %q = %q",
					fd.URL, from, to)
			}
		}
		if IsLocalFeed(fd.URL) && !strings.HasPrefix(LocalFeedPath(fd.URL), "/") {
			return Errorf(EINVALID,
				"feed %s: local feed path must be absolute (the systemd unit's working directory is not yours)", fd.URL)
		}
		if seenFeed[fd.URL] {
			return Errorf(EINVALID, "duplicate feed url %s", fd.URL)
		}
		seenFeed[fd.URL] = true
		if fd.Body != "" && !validBodyScopes[fd.Body] {
			return Errorf(EINVALID, "feed %s: body %q invalid", fd.URL, fd.Body)
		}
		if fd.ExcerptImage != "" && !validExcerptImages[fd.ExcerptImage] {
			return Errorf(EINVALID, "feed %s: excerpt_image %q invalid", fd.URL, fd.ExcerptImage)
		}
		if !haveAll {
			for _, cat := range fd.Categories {
				if !selected[strings.ToLower(cat)] {
					return Errorf(EINVALID,
						"feed %s: category %q is not selected by any output and there is no ALL river",
						fd.URL, cat)
				}
			}
		}
	}

	return nil
}
