.PHONY: build build-frontend build-go build-desktop release test test-go test-front test-e2e lint fmt tidy clean run run-browser cross-check install-dev-linux pss licenses package-linux package-native check-packaging

BIN_DIR := build/bin
BIN     := $(BIN_DIR)/spk-mm-client
DIST    := cmd/spk-mm-client/dist
DESKTOP_TAGS := wails gtk3
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS := -w -s -X main.version=$(VERSION)

define with_dist
	rm -rf $(DIST) && mkdir -p $(DIST) && cp -r frontend/dist/. $(DIST)/
	$(1)
	rm -rf $(DIST) && mkdir -p $(DIST) && touch $(DIST)/.gitkeep
endef

build: build-frontend build-go

build-frontend:
	cd frontend && pnpm install --frozen-lockfile --silent && pnpm build
	python3 packaging/licenses.py

# Browser-mode binary: pure Go (modernc SQLite), no cgo. Desktop keeps cgo (GTK/WebKit).
build-go:
	mkdir -p $(BIN_DIR)
	$(call with_dist,CGO_ENABLED=0 go build -trimpath -ldflags="$(LDFLAGS)" -o $(BIN) ./cmd/spk-mm-client)

# Builds to a temp file and mv's it into place atomically: a running
# instance keeps its old (now-unlinked) inode alive across the replace
# (see `make run`), and a failed build never touches the existing binary.
build-desktop: build-frontend
	mkdir -p $(BIN_DIR)
	$(call with_dist,CGO_ENABLED=1 go build -tags "$(DESKTOP_TAGS)" -trimpath -ldflags="$(LDFLAGS)" -o $(BIN_DIR)/spk-mm-client-desktop.tmp ./cmd/spk-mm-client)
	mv -f $(BIN_DIR)/spk-mm-client-desktop.tmp $(BIN_DIR)/spk-mm-client-desktop

release: build-frontend
	mkdir -p $(BIN_DIR)
	$(call with_dist,CGO_ENABLED=1 go build -tags "$(DESKTOP_TAGS) production" -trimpath -ldflags="$(LDFLAGS)" -o $(BIN_DIR)/spk-mm-client-release ./cmd/spk-mm-client)

# Windows desktop build needs no cgo (WebView2 via pure Go) — cross-compiles
# from Linux and catches Windows-only compile errors early. macOS needs cgo +
# SDK and is covered by CI in stage 4.
cross-check:
	GOOS=windows GOARCH=amd64 CGO_ENABLED=0 go build -tags wails -o /dev/null ./cmd/spk-mm-client
	GOOS=windows GOARCH=amd64 CGO_ENABLED=0 go vet -tags wails ./...

test: test-go test-front test-e2e

test-go:
	go test -race -timeout 120s ./...

test-front:
	cd frontend && pnpm test

test-e2e: build
	cd tests/e2e && pnpm install --silent && pnpm exec playwright install chromium && pnpm exec playwright test

lint:
	go vet ./...
	golangci-lint run
	cd frontend && pnpm lint

fmt:
	go fmt ./...

tidy:
	go mod tidy

clean:
	rm -rf build frontend/dist

# Rebuilds everything (frontend + desktop binary, build-desktop's temp+mv)
# and runs the fresh binary in the foreground. Does not stop an already
# running instance — Ctrl+C the old one first (SIGINT/SIGTERM both quit it
# cleanly within ~1s: internal/desktop/run.go, cmd/spk-mm-client/main.go).
run: build-desktop
	$(BIN_DIR)/spk-mm-client-desktop

run-browser: build
	H=$$(mktemp -d); SPK_MM_CLIENT_HOME=$$H SPK_MM_CLIENT_DOWNLOADS=$$H/dl $(BIN) --browser --port=5180 --mm-fake --test-api

install-dev-linux: build-desktop
	bash scripts/install-dev-linux.sh $(abspath $(BIN_DIR)/spk-mm-client-desktop)

pss:
	bash scripts/pss.sh $(PID)

licenses: build-frontend
	python3 packaging/licenses.py --check

package-linux:
	python3 packaging/release.py --version "$(RELEASE_VERSION)" --arch "$(ARCH)"

package-native:
	python3 packaging/portable.py --version "$(RELEASE_VERSION)" --os "$(TARGET_OS)" --arch "$(ARCH)"

check-packaging:
	python3 -m unittest discover -s packaging/tests -v
