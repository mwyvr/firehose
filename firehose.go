// Package firehose defines the domain types and service interfaces for firehose, the application.
// firehose, the application, is an RSS aggregator that produces static HTML in a river-of-news format.
package firehose

import (
	"errors"
	"fmt"
)

// Application error codes.
const (
	ECONFLICT = "conflict"  // action cannot proceed against current state
	EINTERNAL = "internal"  // unexpected internal error
	EINVALID  = "invalid"   // validation failed / malformed input
	ENOTFOUND = "not_found" // entity not found (e.g. HTTP 404 feed)
	EPANIC    = "panic"     // recovered per-feed panic
	EPARSE    = "parse"     // fetched OK but content would not parse (bad XML)
	ETIMEOUT  = "timeout"   // fetch timed out
)

// Error is a domain error carrying a machine-readable Code and a
// human-readable Message. Mirrors the WTF error pattern.
type Error struct {
	Code    string
	Message string
}

// Error implements the error interface.
func (e *Error) Error() string {
	return fmt.Sprintf("firehose error: code=%s message=%s", e.Code, e.Message)
}

// ErrorCode unwraps an application error code from err, or returns EINTERNAL if
// none is present, or "" if err is nil.
func ErrorCode(err error) string {
	var e *Error
	switch {
	case err == nil:
		return ""
	case errors.As(err, &e):
		return e.Code
	default:
		return EINTERNAL
	}
}

// Errorf constructs an *Error with a formatted message.
func Errorf(code, format string, args ...any) *Error {
	return &Error{
		Code:    code,
		Message: fmt.Sprintf(format, args...),
	}
}

// Version is the build version, injected at build time via
// -ldflags "-X github.com/mwyvr/firehose.Version=..." (see Makefile).
// Plain `go build` produces "dev". Shown by -h, `firehose version`, and the
// page footer.
var Version = "dev"
