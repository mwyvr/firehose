package sqlite

import (
	"errors"
	"strings"
	"testing"
)

func TestOpenHintNamesUserAndDir(t *testing.T) {
	err := errors.New("unable to open database file: out of memory (14)")
	hint := explainOpenError("file:/var/lib/firehose/cache.db", err)
	if !strings.Contains(hint, "/var/lib/firehose") {
		t.Errorf("hint missing directory: %q", hint)
	}
	if !strings.Contains(hint, "sudo -u firehose") {
		t.Errorf("hint missing remedy: %q", hint)
	}
	if explainOpenError("file:x.db", errors.New("some other failure")) != "" {
		t.Error("hint must only decorate CANTOPEN-class failures")
	}
}
