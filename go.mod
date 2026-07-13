module github.com/mwyvr/firehose

go 1.25.0

require (
	github.com/BurntSushi/toml v1.6.0
	github.com/alecthomas/chroma/v2 v2.14.0
	github.com/andybalholm/cascadia v1.3.3
	github.com/microcosm-cc/bluemonday v1.0.27
	github.com/mmcdole/gofeed v1.4.0
	github.com/mwyvr/kid v1.3.1
	golang.org/x/net v0.56.0
	modernc.org/sqlite v1.34.1
)

require (
	github.com/aymerick/douceur v0.2.0 // indirect
	github.com/dlclark/regexp2 v1.11.0 // indirect
	github.com/dustin/go-humanize v1.0.1 // indirect
	github.com/google/uuid v1.6.0 // indirect
	github.com/gorilla/css v1.0.1 // indirect
	github.com/hashicorp/golang-lru/v2 v2.0.7 // indirect
	github.com/mattn/go-isatty v0.0.20 // indirect
	github.com/mmcdole/goxpp/v2 v2.0.0 // indirect
	github.com/ncruces/go-strftime v0.1.9 // indirect
	github.com/remyoudompheng/bigfft v0.0.0-20230129092748-24d4a6f8daec // indirect
	golang.org/x/sys v0.46.0 // indirect
	golang.org/x/text v0.38.0 // indirect
	modernc.org/gc/v3 v3.0.0-20240107210532-573471604cb6 // indirect
	modernc.org/libc v1.55.3 // indirect
	modernc.org/mathutil v1.6.0 // indirect
	modernc.org/memory v1.8.0 // indirect
	modernc.org/strutil v1.2.0 // indirect
	modernc.org/token v1.1.0 // indirect
)

// All dependencies are pure Go; the build is CGO_ENABLED=0-clean and the
// Makefile tripwire enforces it so a future cgo dependency fails the build.
