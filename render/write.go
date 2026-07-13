package render

import (
	"os"
	"path/filepath"

	"github.com/mwyvr/firehose"
)

// atomicWrite writes data to path via a temp file + rename so a reader never
// sees a truncated page. Files land world-readable (0644, dirs 0755) by
// design: the output is public web content served by a different user.
func atomicWrite(path string, data []byte) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return firehose.Errorf(firehose.EINTERNAL, "render: mkdir %s: %v", dir, err)
	}
	tmp, err := os.CreateTemp(dir, ".firehose-*")
	if err != nil {
		return firehose.Errorf(firehose.EINTERNAL, "render: temp file in %s: %v", dir, err)
	}
	tmpName := tmp.Name()
	defer func() { _ = os.Remove(tmpName) }() // no-op after successful rename

	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return firehose.Errorf(firehose.EINTERNAL, "render: write %s: %v", tmpName, err)
	}
	if err := tmp.Chmod(0o644); err != nil {
		_ = tmp.Close()
		return firehose.Errorf(firehose.EINTERNAL, "render: chmod %s: %v", tmpName, err)
	}
	if err := tmp.Close(); err != nil {
		return firehose.Errorf(firehose.EINTERNAL, "render: close %s: %v", tmpName, err)
	}
	if err := os.Rename(tmpName, path); err != nil {
		return firehose.Errorf(firehose.EINTERNAL, "render: rename to %s: %v", path, err)
	}
	return nil
}
