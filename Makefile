PREFIX ?= /usr/local
GORELEASER ?= goreleaser
DESTDIR ?=
SYSCONFDIR ?= /etc
UNITDIR ?= $(SYSCONFDIR)/systemd/system
LOGROTATEDIR ?= $(SYSCONFDIR)/logrotate.d
PORT ?= 8082

.DEFAULT_GOAL := help
.PHONY: help all demo run test test-browser test-imvault check fmt build clean install install-openrc install-systemd install-logrotate release-check release-snapshot

help: ## Show available commands
	@printf '\nWitmoot\n\n'
	@awk 'BEGIN {FS = ":.*## "} /^[a-zA-Z0-9_-]+:.*## / {printf "  %-20s %s\n", $$1, $$2}' $(MAKEFILE_LIST)
	@printf '\nTry it: make demo (or make demo PORT=9000)\n'
	@printf 'Install paths: PREFIX=%s SYSCONFDIR=%s; DESTDIR supports staging.\n\n' "$(PREFIX)" "$(SYSCONFDIR)"

all: check build ## Run checks and build the server

demo: ## Start a disposable board with sample conversations and logins
	go run -buildvcs=false ./scripts/demo -port "$(PORT)"

run: ## Run your board using WITMOOT_* settings (default data: ./data)
	go run -buildvcs=false ./cmd/witmoot

test: ## Run Go tests with the race detector
	go test -race -count=1 ./...

test-browser: build ## Run browser checks (needs Node and Playwright; see README)
	node scripts/browser/check.mjs

test-imvault: build ## Test image integration (set IMVAULT_BINARY; see README)
	node scripts/browser/imvault.mjs

check: ## Run vet, race tests, and formatting checks
	go vet ./...
	go test -race -count=1 ./...
	@test -z "$$(gofmt -l cmd internal contrib scripts)" || { echo 'Run gofmt on Go sources'; exit 1; }

fmt: ## Format Go sources
	gofmt -w cmd internal contrib scripts

build: ## Build the server into bin/witmoot
	mkdir -p bin
	CGO_ENABLED=0 go build -buildvcs=false -trimpath -o bin/witmoot ./cmd/witmoot

clean: ## Remove generated binaries
	rm -rf bin

release-check: ## Validate the GoReleaser configuration
	"$(GORELEASER)" check

release-snapshot: release-check ## Build local archives and Debian packages without publishing
	"$(GORELEASER)" release --snapshot --clean --skip=publish

# Service targets install configuration only. Combine with "install" for the binary.
install: build ## Install the binary and documentation
	install -d "$(DESTDIR)$(PREFIX)/bin"
	install -m 0755 bin/witmoot "$(DESTDIR)$(PREFIX)/bin/witmoot"
	install -d "$(DESTDIR)$(PREFIX)/share/doc/witmoot"
	install -m 0644 LICENSE README.md THIRD_PARTY.md "$(DESTDIR)$(PREFIX)/share/doc/witmoot/"

install-openrc: install-logrotate ## Install OpenRC configuration and log rotation
	mkdir -p bin
	@bindir=$$(printf '%s\n' "$(PREFIX)/bin" | sed 's/[\&|]/\\&/g') || exit 1; \
		sed "s|/usr/local/bin|$$bindir|g" contrib/openrc/witmoot > bin/witmoot.openrc
	install -d "$(DESTDIR)$(SYSCONFDIR)/init.d" "$(DESTDIR)$(SYSCONFDIR)/conf.d"
	install -m 0755 bin/witmoot.openrc "$(DESTDIR)$(SYSCONFDIR)/init.d/witmoot"
	@sh scripts/install-config.sh contrib/openrc/witmoot.confd "$(DESTDIR)$(SYSCONFDIR)/conf.d/witmoot" 0600

install-systemd: ## Install systemd configuration
	mkdir -p bin
	@bindir=$$(printf '%s\n' "$(PREFIX)/bin" | sed 's/[\&|]/\\&/g') || exit 1; \
		confdir=$$(printf '%s\n' "$(SYSCONFDIR)" | sed 's/[\&|]/\\&/g') || exit 1; \
		sed -e "s|/usr/local/bin|$$bindir|g" -e "s|/etc/witmoot|$$confdir/witmoot|g" \
			contrib/systemd/witmoot.service > bin/witmoot.service
	install -d "$(DESTDIR)$(UNITDIR)" "$(DESTDIR)$(SYSCONFDIR)/witmoot"
	install -m 0644 bin/witmoot.service "$(DESTDIR)$(UNITDIR)/witmoot.service"
	@sh scripts/install-config.sh contrib/systemd/witmoot.env "$(DESTDIR)$(SYSCONFDIR)/witmoot/witmoot.env" 0600

install-logrotate: ## Install log rotation while preserving local settings
	install -d "$(DESTDIR)$(LOGROTATEDIR)"
	@sh scripts/install-config.sh contrib/logrotate/witmoot "$(DESTDIR)$(LOGROTATEDIR)/witmoot" 0644
	@if [ -z "$(DESTDIR)" ] && ! command -v logrotate >/dev/null 2>&1; then \
		echo 'Warning: install logrotate and enable its cron job or timer to rotate Witmoot logs.' >&2; \
	fi
