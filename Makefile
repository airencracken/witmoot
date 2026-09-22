PREFIX ?= /usr/local
GORELEASER ?= goreleaser
DESTDIR ?=
SYSCONFDIR ?= /etc
UNITDIR ?= $(SYSCONFDIR)/systemd/system
LOGROTATEDIR ?= $(SYSCONFDIR)/logrotate.d

.PHONY: run test test-browser test-imvault check build install install-openrc install-systemd install-logrotate release-check release-snapshot

run:
	go run -buildvcs=false ./cmd/witmoot

test:
	go test -race -count=1 ./...

test-browser: build
	node scripts/browser/check.mjs

test-imvault: build
	node scripts/browser/imvault.mjs

check:
	go vet ./...
	go test -race -count=1 ./...
	@test -z "$$(gofmt -l cmd internal contrib scripts)" || { echo 'Run gofmt on Go sources'; exit 1; }

build:
	mkdir -p bin
	CGO_ENABLED=0 go build -buildvcs=false -trimpath -o bin/witmoot ./cmd/witmoot

release-check:
	"$(GORELEASER)" check

release-snapshot: release-check
	"$(GORELEASER)" release --snapshot --clean --skip=publish

# Service targets install configuration only. Combine with "install" for the binary.
install: build
	install -d "$(DESTDIR)$(PREFIX)/bin"
	install -m 0755 bin/witmoot "$(DESTDIR)$(PREFIX)/bin/witmoot"
	install -d "$(DESTDIR)$(PREFIX)/share/doc/witmoot"
	install -m 0644 LICENSE README.md THIRD_PARTY.md "$(DESTDIR)$(PREFIX)/share/doc/witmoot/"

install-openrc: install-logrotate
	mkdir -p bin
	@bindir=$$(printf '%s\n' "$(PREFIX)/bin" | sed 's/[\&|]/\\&/g') || exit 1; \
		sed "s|/usr/local/bin|$$bindir|g" contrib/openrc/witmoot > bin/witmoot.openrc
	install -d "$(DESTDIR)$(SYSCONFDIR)/init.d" "$(DESTDIR)$(SYSCONFDIR)/conf.d"
	install -m 0755 bin/witmoot.openrc "$(DESTDIR)$(SYSCONFDIR)/init.d/witmoot"
	@sh scripts/install-config.sh contrib/openrc/witmoot.confd "$(DESTDIR)$(SYSCONFDIR)/conf.d/witmoot" 0600

install-systemd:
	mkdir -p bin
	@bindir=$$(printf '%s\n' "$(PREFIX)/bin" | sed 's/[\&|]/\\&/g') || exit 1; \
		confdir=$$(printf '%s\n' "$(SYSCONFDIR)" | sed 's/[\&|]/\\&/g') || exit 1; \
		sed -e "s|/usr/local/bin|$$bindir|g" -e "s|/etc/witmoot|$$confdir/witmoot|g" \
			contrib/systemd/witmoot.service > bin/witmoot.service
	install -d "$(DESTDIR)$(UNITDIR)" "$(DESTDIR)$(SYSCONFDIR)/witmoot"
	install -m 0644 bin/witmoot.service "$(DESTDIR)$(UNITDIR)/witmoot.service"
	@sh scripts/install-config.sh contrib/systemd/witmoot.env "$(DESTDIR)$(SYSCONFDIR)/witmoot/witmoot.env" 0600

install-logrotate:
	install -d "$(DESTDIR)$(LOGROTATEDIR)"
	@sh scripts/install-config.sh contrib/logrotate/witmoot "$(DESTDIR)$(LOGROTATEDIR)/witmoot" 0644
	@if [ -z "$(DESTDIR)" ] && ! command -v logrotate >/dev/null 2>&1; then \
		echo 'Warning: install logrotate and enable its cron job or timer to rotate Witmoot logs.' >&2; \
	fi
