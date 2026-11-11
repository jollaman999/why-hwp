# Works with GNU make on Linux, macOS and Windows (Git Bash).

BINARY    := why-hwp
PKG       := ./cmd/why-hwp
VERSION   ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS   := -s -w -X main.version=$(VERSION)
GOEXE     := $(shell go env GOEXE)
PLATFORMS := windows/amd64 windows/arm64 linux/amd64 linux/arm64 darwin/amd64 darwin/arm64

export CGO_ENABLED := 0

.PHONY: build dist checksums test vet fmt clean

build:
	go build -trimpath -ldflags "$(LDFLAGS)" -o bin/$(BINARY)$(GOEXE) $(PKG)

dist:
	@mkdir -p dist
	@set -e; for p in $(PLATFORMS); do \
		os=$${p%/*}; arch=$${p#*/}; ext=; \
		if [ "$$os" = windows ]; then ext=.exe; fi; \
		out=dist/$(BINARY)-$$os-$$arch$$ext; \
		echo "build $$out"; \
		GOOS=$$os GOARCH=$$arch go build -trimpath -ldflags "$(LDFLAGS)" -o $$out $(PKG); \
	done

checksums: dist
	@set -e; cd dist; \
	if command -v sha256sum >/dev/null 2>&1; then sum="sha256sum"; else sum="shasum -a 256"; fi; \
	$$sum $(BINARY)-* > SHA256SUMS; \
	cat SHA256SUMS

test:
	go test ./...

vet:
	go vet ./...

fmt:
	gofmt -l -w .

clean:
	rm -rf bin dist
