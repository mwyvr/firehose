package feed

import (
	"errors"
	"io"
	"os"
	"time"

	"github.com/mmcdole/gofeed"
	"github.com/mwyvr/firehose"
)

// fetchLocal handles file:// feeds
func (f *Fetcher) fetchLocal(fd *firehose.Feed) (res result) {
	res.feed = fd

	path := firehose.LocalFeedPath(fd.URL)
	fi, err := os.Stat(path)
	if err != nil {
		code := firehose.EINTERNAL
		if errors.Is(err, os.ErrNotExist) {
			code = firehose.ENOTFOUND
		}
		res.upd = f.failure(fd, code)
		return res
	}

	now := f.Now()
	mtime := fi.ModTime().UTC().Format(time.RFC3339Nano)
	if fd.LastModified != "" && fd.LastModified == mtime {
		res.upd = success(now, false) // unchanged: the local 304
		return res
	}

	fh, err := os.Open(path)
	if err != nil {
		res.upd = f.failure(fd, firehose.EINTERNAL)
		return res
	}
	defer func() { _ = fh.Close() }()

	parsed, err := gofeed.NewParser().Parse(io.LimitReader(fh, maxBodyBytes))
	if err != nil {
		res.upd = f.failure(fd, firehose.EPARSE)
		return res
	}

	strip, err := compileStrip(fd.StripSelectors)
	if err != nil {
		res.upd = f.failure(fd, firehose.EINVALID)
		return res
	}

	res.items = f.convert(fd, parsed, now, strip)
	res.upd = success(now, len(res.items) > 0)
	res.upd.LastModified = &mtime
	persistSelfTitle(&res.upd, fd, parsed)
	return res
}
