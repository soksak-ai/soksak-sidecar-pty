SHELL := /bin/sh
OUT ?= dist
BUILD_ROOT ?= target

.PHONY: require-target preflight prepare build stage verify

require-target:
	@test '$(origin TARGET)' = 'command line' && test -n '$(TARGET)' || { echo 'TARGET must be an explicit Make command-line variable' >&2; exit 2; }

preflight: require-target
	@scripts/check-build-environment.sh '$(TARGET)'

prepare: preflight
	@GOFLAGS=-mod=readonly go mod download
	@go mod verify

build: prepare
	@set -- $$(scripts/resolve-target.sh '$(TARGET)'); \
		goos=$$1; goarch=$$2; extension=$$3; test "$$extension" != none || extension=; \
		output='$(BUILD_ROOT)/$(TARGET)/release/soksak-sidecar-pty'$$extension; \
		mkdir -p "$$(dirname "$$output")"; \
		next=$$output.next.$$$$; \
		trap 'rm -f "$$next"' EXIT HUP INT TERM; \
		CGO_ENABLED=0 GOOS=$$goos GOARCH=$$goarch go build -mod=readonly -trimpath -buildvcs=false -o "$$next" .; \
		chmod +x "$$next"; \
		mv -f "$$next" "$$output"; \
		go version -m "$$output" | grep -F "GOOS=$$goos" >/dev/null; \
		go version -m "$$output" | grep -F "GOARCH=$$goarch" >/dev/null; \
		printf 'PTY_BUILD_READY target=%s output=%s\n' '$(TARGET)' "$$output"

stage: build
	@./stage.sh '$(OUT)' '$(TARGET)' '$(BUILD_ROOT)'

verify: build
	@go mod tidy -diff
	@go test -count=1 ./...
	@go vet ./...
