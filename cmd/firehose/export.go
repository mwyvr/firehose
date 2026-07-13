package main

import (
	"encoding/xml"
	"flag"
	"fmt"
	"os"

	"github.com/mwyvr/firehose"
)

// runExport writes the feed list as OPML to stdout. Document assembly is
// domain logic (firehose.BuildOPML); this is flags and encoding only.
func runExport(configPath string, args []string) error {
	fs := flag.NewFlagSet("export", flag.ContinueOnError)
	section := fs.String("section", "", "export only this section (output name)")
	if err := fs.Parse(args); err != nil {
		return err
	}

	cfg, err := loadAndReport(configPath)
	if err != nil {
		return err
	}

	doc := firehose.BuildOPML(cfg, *section)
	enc := xml.NewEncoder(os.Stdout)
	enc.Indent("", "  ")
	if _, err := fmt.Print(xml.Header); err != nil {
		return err
	}
	if err := enc.Encode(doc); err != nil {
		return err
	}
	fmt.Println()
	return nil
}
