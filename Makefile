# firehose — make produces a single static binary, no CGO

VERSION := $(shell git describe --tags --always --dirty 2>/dev/null || echo v0.0.0-dev)
-include .env
HOST    ?= 
LDFLAGS := -ldflags "-X github.com/mwyvr/firehose.Version=$(VERSION)"

BIN    := bin/firehose
LINUX  := bin/firehose-linux-amd64
PKG    := ./cmd/firehose
DEVDIR := build/dev

.PHONY: all fmt vet test build vps dev deploy tidy snapshot clean edit bump force

all: fmt vet test build

fmt:
	@out="$$(gofmt -l .)"; if [ -n "$$out" ]; then echo "gofmt needed:"; echo "$$out"; exit 1; fi

vet:
	go vet ./...

test:
	go test ./...

build: tidy
	CGO_ENABLED=0 go build $(LDFLAGS) -o $(BIN) $(PKG)
	@go version -m $(BIN) | grep -q 'CGO_ENABLED=0' \
		&& echo "tripwire ok: $(BIN) built without cgo" \
		|| { echo "TRIPWIRE: $(BIN) was built with cgo"; exit 1; }

# Cross-compile for lowest common denominator Virtual Private Server - often
# QEMU with older architecture x86-64-v2 vs v3, for example.
vps: tidy
	CGO_ENABLED=0 GOOS=linux GOARCH=amd64 GOAMD64=v2 go build $(LDFLAGS) -o $(LINUX) $(PKG)
	@go version -m $(LINUX) | grep -q 'CGO_ENABLED=0' \
		&& echo "tripwire ok: $(LINUX) built without cgo" \
		|| { echo "TRIPWIRE: $(LINUX) was built with cgo"; exit 1; }

tidy:
	go mod tidy

# Local goreleaser dry-run: builds the full release matrix into dist/
# requires: goreleaser v2 installed
snapshot:
	goreleaser release --snapshot --clean

# Build for the VPS, upload, install, verify the deployed version, and kick an
# immediate run.
deploy: vps
	scp $(LINUX) $(HOST):/tmp/firehose.new
	ssh -t $(HOST) 'sudo install -m 0755 /tmp/firehose.new /usr/local/bin/firehose \
		&& rm -f /tmp/firehose.new \
		&& echo -n "deployed: " && /usr/local/bin/firehose version \
		&& sudo systemctl start firehose.service \
		&& systemctl status firehose.service --no-pager -n 0'

# Fetch the sample feeds (testdata/dev-config.toml) into a disposable
# directory and render the rivers there. Everything lands in build/dev.
dev: build
	@mkdir -p $(DEVDIR)
	@cp testdata/dev-config.toml $(DEVDIR)/config.toml
	./$(BIN) -config $(DEVDIR)/config.toml check
	./$(BIN) -config $(DEVDIR)/config.toml
	@echo ""
	@echo "dev run complete — open $(DEVDIR)/index.html  (health: $(DEVDIR)/firehose.html)"

# edit the remote config, check after
edit:
	ssh -t $(HOST) '$(EDITOR) /etc/firehose/config.toml \
		&& /usr/local/bin/firehose check'

# restart the service, run respecting politeness
bump:
	ssh -t $(HOST) 'systemctl restart firehose.service \
		&& systemctl status firehose.service --no-pager -n 0'

# bypass systemd, force full refresh
force:
	ssh -t $(HOST) 'sudo /usr/local/bin/firehose -force \
		&& systemctl status firehose.service --no-pager -n 0'

purge:
	ssh -t $(HOST) 'sudo rm /var/lib/firehose/cache.db'
	$(MAKE) bump
	
clean:
	rm -rf bin build dist
