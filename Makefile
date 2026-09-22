.PHONY: run test test-browser check build

run:
	go run -buildvcs=false ./cmd/witmoot

test:
	go test -race ./...

test-browser: build
	node scripts/browser/check.mjs

check:
	go vet ./...
	go test -race ./...
	@test -z "$$(gofmt -l cmd internal)" || { echo 'Run gofmt on Go sources'; exit 1; }

build:
	mkdir -p bin
	CGO_ENABLED=0 go build -buildvcs=false -trimpath -o bin/witmoot ./cmd/witmoot
