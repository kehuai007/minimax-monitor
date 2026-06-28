BIN := dist/minimax-monitor
LDFLAGS := -s -w
PKG := ./cmd/minimax-monitor

.PHONY: build build-all run test clean

build:
	mkdir -p dist
	CGO_ENABLED=0 go build -trimpath -ldflags="$(LDFLAGS)" -o $(BIN) $(PKG)

build-all:
	CGO_ENABLED=0 GOOS=linux  GOARCH=amd64 go build -trimpath -ldflags="$(LDFLAGS)" -o dist/minimax-monitor-linux-amd64    $(PKG)
	CGO_ENABLED=0 GOOS=linux  GOARCH=arm64 go build -trimpath -ldflags="$(LDFLAGS)" -o dist/minimax-monitor-linux-arm64    $(PKG)
	CGO_ENABLED=0 GOOS=darwin GOARCH=arm64 go build -trimpath -ldflags="$(LDFLAGS)" -o dist/minimax-monitor-darwin-arm64   $(PKG)
	CGO_ENABLED=0 GOOS=windows GOARCH=amd64 go build -trimpath -ldflags="$(LDFLAGS)" -o dist/minimax-monitor-windows-amd64.exe $(PKG)

run: build
	./$(BIN)

test:
	go test ./...

clean:
	rm -rf dist