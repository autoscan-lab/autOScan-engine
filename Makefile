build:
	go build ./...

build-bridge:
	mkdir -p dist
	go build -o dist/autoscan-bridge ./cmd/autoscan-bridge

vet:
	go vet ./...

test:
	go test ./...

tidy:
	go mod tidy

check: build vet test

clean:
	go clean -cache -testcache -modcache

.PHONY: build build-bridge vet test tidy check clean
