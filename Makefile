BINARY=autoscan-engine
INSTALL_PATH=$(HOME)/.local/bin/$(BINARY)
DIST=dist

build:
	go build -o $(BINARY) ./cmd/autoscan-engine

install: build
	@mkdir -p $(HOME)/.local/bin
	cp $(BINARY) $(INSTALL_PATH)
	@echo "Installed to $(INSTALL_PATH)"

uninstall:
	rm -f $(INSTALL_PATH)

clean:
	rm -f $(BINARY)
	rm -rf $(DIST)

release: clean
	@mkdir -p $(DIST)
	@echo "Building macOS arm64..."
	GOOS=darwin GOARCH=arm64 go build -o $(DIST)/$(BINARY)-darwin-arm64 ./cmd/autoscan-engine
	@echo "Building Linux amd64..."
	GOOS=linux GOARCH=amd64 go build -o $(DIST)/$(BINARY)-linux-amd64 ./cmd/autoscan-engine
	@echo ""
	@ls -lh $(DIST)/

windows:
	@mkdir -p $(DIST)
	@echo "Building Windows amd64..."
	GOOS=windows GOARCH=amd64 go build -o $(DIST)/$(BINARY)-windows-amd64.exe ./cmd/autoscan-engine
	@echo ""
	@ls -lh $(DIST)/

.PHONY: build install uninstall clean release windows
