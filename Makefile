export GO111MODULE = on

LDFLAGS := -ldflags "-s -w"
GO_BUILD := go build
GO_TEST := go test
APP_PKG := ./cmd/sigsentinel

.PHONY: build build-gui check-gui-deps clean test test-all test-cover bench lint

build: build-gui

build-gui: check-gui-deps
	@mkdir -p bin
	$(GO_BUILD) $(LDFLAGS) -o bin/sigsentinel $(APP_PKG)

check-gui-deps:
	@if [ "$$(uname -s)" = "Linux" ]; then \
		if ! command -v pkg-config >/dev/null 2>&1; then \
			echo "Missing dependency: pkg-config"; \
			echo "Install (Debian/Ubuntu): sudo apt-get install -y pkg-config"; \
			exit 1; \
		fi; \
		missing=0; \
		for pkg in gl x11 xrandr xi xcursor xinerama xxf86vm; do \
			if ! pkg-config --exists $$pkg; then \
				echo "Missing dependency: $$pkg (pkg-config package)"; \
				missing=1; \
			fi; \
		done; \
		if [ $$missing -ne 0 ]; then \
			echo "Install GUI build deps (Debian/Ubuntu): sudo apt-get install -y libgl1-mesa-dev xorg-dev"; \
			exit 1; \
		fi; \
	fi

clean:
	rm -rf bin/

test:
	$(GO_TEST) -short -tags headless ./...

test-all:
	$(GO_TEST) -race -cover ./...

test-cover:
	$(GO_TEST) -race -coverprofile=test.out ./... && go tool cover --html=test.out

bench:
	$(GO_TEST) --benchmem -benchtime=20s -bench='Benchmark.*' -run='^$$' -tags headless ./...

lint:
	golangci-lint run --timeout=600s && go vet ./...
