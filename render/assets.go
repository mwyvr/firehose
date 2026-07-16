package render

import (
	"bytes"
	"path/filepath"
	"strings"

	chromahtml "github.com/alecthomas/chroma/v2/formatters/html"
	"github.com/alecthomas/chroma/v2/styles"
	"github.com/mwyvr/firehose"
)

// writeAssets writes style.css (fonts injected from config) and river.js.
func (r *Renderer) writeAssets() error {
	var buf bytes.Buffer
	if err := r.css.Execute(&buf, fontsView(r.cfg)); err != nil {
		return firehose.Errorf(firehose.EINTERNAL, "render: css: %v", err)
	}
	if r.cfg.Settings.Highlight {
		if err := writeChromaCSS(&buf, r.cfg.Settings.HighlightTheme, r.cfg.Settings.HighlightThemeDark); err != nil {
			return err
		}
	}
	if err := atomicWrite(filepath.Join(r.cfg.Settings.OutputDir, "style.css"), buf.Bytes()); err != nil {
		return err
	}
	for _, name := range []string{
		"river.js",
		"favicon.svg",
		"favicon.ico",
		"apple-touch-icon.png",
		"apple-touch-icon-precomposed.png",
	} {
		data, err := embedded.ReadFile("assets/" + name)
		if err != nil {
			return firehose.Errorf(firehose.EINTERNAL, "render: read %s: %v", name, err)
		}
		if err := atomicWrite(filepath.Join(r.cfg.Settings.OutputDir, name), data); err != nil {
			return err
		}
	}
	return nil
}

func writeChromaCSS(buf *bytes.Buffer, light, dark string) error {
	fmter := chromahtml.New(chromahtml.WithClasses(true))
	buf.WriteString("\n/* chroma syntax highlighting */\n")
	if err := fmter.WriteCSS(buf, styles.Get(light)); err != nil {
		return firehose.Errorf(firehose.EINTERNAL, "render: chroma css %s: %v", light, err)
	}
	var darkCSS bytes.Buffer
	if err := fmter.WriteCSS(&darkCSS, styles.Get(dark)); err != nil {
		return firehose.Errorf(firehose.EINTERNAL, "render: chroma css %s: %v", dark, err)
	}
	buf.WriteString("@media (prefers-color-scheme: dark) {\n")
	buf.WriteString(prefixSelectors(darkCSS.String(), ":root:not([data-theme=\"light\"]) "))
	buf.WriteString("}\n")
	buf.WriteString(prefixSelectors(darkCSS.String(), ":root[data-theme=\"dark\"] "))
	return nil
}

// prefixSelectors scopes every rule in a chroma-emitted stylesheet under
// prefix. Chroma's WriteCSS emits one rule per line, optionally preceded by
// a same-line comment: `/* Name */ .chroma .k { ... }`. Lines without a rule
// pass through untouched.
func prefixSelectors(css, prefix string) string {
	lines := strings.Split(css, "\n")
	for i, line := range lines {
		rest := line
		lead := ""
		if idx := strings.Index(rest, "*/"); strings.HasPrefix(strings.TrimSpace(rest), "/*") && idx >= 0 {
			lead = rest[:idx+2] + " "
			rest = strings.TrimSpace(rest[idx+2:])
		}
		brace := strings.Index(rest, "{")
		if brace <= 0 {
			continue // not a rule line
		}
		sels := strings.Split(rest[:brace], ",")
		for j, s := range sels {
			sels[j] = prefix + strings.TrimSpace(s)
		}
		lines[i] = lead + strings.Join(sels, ", ") + " " + rest[brace:]
	}
	return strings.Join(lines, "\n")
}

// fontsCtx is the stylesheet template's context: FontConfig plus the
// resolved remote URL.
type fontsCtx struct {
	firehose.FontConfig
	CSSURL string
}

func fontsView(cfg *firehose.Config) fontsCtx {
	return fontsCtx{FontConfig: cfg.Fonts, CSSURL: cfg.FontsCSSURL()}
}
