build:
	go build ./...

vet:
	go vet ./...

test:
	go test ./...

tidy:
	go mod tidy

check: build vet test

clean:
	go clean -cache -testcache -modcache

.PHONY: build vet test tidy check clean
