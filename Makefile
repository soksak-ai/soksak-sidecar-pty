SHELL := /bin/sh
OUT ?= dist
BUILD_ROOT ?= target
SDK_VERSION := 0.0.15

.PHONY: require-target preflight lock prepare build stage verify require-tooling require-out release attest

require-target:
	@test '$(origin TARGET)' = 'command line' && test -n '$(TARGET)' || { echo 'TARGET must be an explicit Make command-line variable' >&2; exit 2; }

preflight: require-target
	@scripts/check-build-environment.sh '$(TARGET)'

lock: preflight
	@go mod tidy

prepare: preflight
	@GOFLAGS=-mod=readonly go mod download
	@go mod verify

build: prepare
	@set -eu; set -- $$(scripts/resolve-target.sh '$(TARGET)'); \
		goos=$$1; goarch=$$2; extension=$$3; test "$$extension" != none || extension=; \
		cgo=0; test "$$goos" != darwin || cgo=1; \
		output='$(BUILD_ROOT)/$(TARGET)/release/soksak-sidecar-pty'$$extension; \
		mkdir -p "$$(dirname "$$output")"; \
		next=$$output.next.$$$$; \
		trap 'rm -f "$$next"' EXIT HUP INT TERM; \
		CGO_ENABLED=$$cgo GOOS=$$goos GOARCH=$$goarch go build -mod=readonly -trimpath -buildvcs=false -o "$$next" .; \
		chmod +x "$$next"; \
		mv -f "$$next" "$$output"; \
		go version -m "$$output" | grep -F "GOOS=$$goos" >/dev/null; \
		go version -m "$$output" | grep -F "GOARCH=$$goarch" >/dev/null; \
		go version -m "$$output" | grep -F "CGO_ENABLED=$$cgo" >/dev/null; \
		printf 'PTY_BUILD_READY target=%s output=%s\n' '$(TARGET)' "$$output"

stage: build
	@./stage.sh '$(OUT)' '$(TARGET)' '$(BUILD_ROOT)'

verify: build
	@go mod tidy -diff
	@go test -count=1 ./...
	@go vet ./...

require-tooling:
	@tool="$$(command -v soksak-sdk)" || { echo 'soksak-sdk is not selected by PATH' >&2; exit 78; }; \
		case "$$tool" in /*) ;; *) echo 'soksak-sdk PATH entry must be absolute' >&2; exit 78 ;; esac; \
		root="$$(cd "$$(dirname "$$tool")/.." && pwd -P)"; \
		test -f "$$tool" && test ! -L "$$tool" && test -f "$$root/release.json" && test ! -L "$$root/release.json" && test -d "$$root/.dependencies/soksak-spec" || { echo 'soksak-sdk PATH entry is not a prepared release' >&2; exit 78; }; \
		package_version="$$(node -e 'process.stdout.write(require(process.argv[1]).version)' "$$root/package.json")"; \
		release_version="$$(node -e 'process.stdout.write(require(process.argv[1]).version)' "$$root/release.json")"; \
		test "$$package_version" = "$(SDK_VERSION)" && test "$$release_version" = "$(SDK_VERSION)" || { echo "TOOLCHAIN_MISMATCH soksak-sdk required=$(SDK_VERSION) package=$$package_version release=$$release_version" >&2; exit 78; }

require-out:
	@case "$(origin OUT)" in "command line") ;; *) echo 'OUT must be an absolute command-line path to the complete release output' >&2; exit 64 ;; esac
	@case "$(OUT)" in /*) ;; *) echo 'OUT must be an absolute path' >&2; exit 64 ;; esac
	@test "$(OUT)" != "$(CURDIR)" || { echo 'OUT must not replace the source repository' >&2; exit 64; }

release: require-tooling require-out verify
	@test -z "$$(git status --porcelain)" || { echo 'release source checkout must be clean' >&2; exit 65; }
	@set -eu; tool="$$(command -v soksak-sdk)"; tooling_root="$$(cd "$$(dirname "$$tool")/.." && pwd -P)"; \
		temp_root="$$(node -e 'const {realpathSync}=require("node:fs");const {tmpdir}=require("node:os");process.stdout.write(realpathSync(tmpdir()))')"; \
		work="$$(mktemp -d "$$temp_root/soksak-sidecar-release.XXXXXX")"; trap 'rm -rf "$$work"' EXIT HUP INT TERM; \
		stage="$$work/stage"; package="$$work/package"; artifacts="$$work/artifacts"; \
		mkdir -p "$$stage" "$$package/dist" "$$artifacts"; \
		./stage.sh "$$stage" '$(TARGET)' '$(BUILD_ROOT)'; \
		cp "$$stage/sidecar.json" "$$package/sidecar.json"; \
		cp LICENSE "$$package/"; \
		cp "$$stage/soksak-sidecar-pty"* "$$package/dist/"; \
		version="$$(node -e 'const {readFileSync}=require("node:fs");process.stdout.write(JSON.parse(readFileSync(process.argv[1],"utf8")).version)' "$(CURDIR)/sidecar.json")"; \
		archive="$$artifacts/soksak-sidecar-pty-$$version-$(TARGET).tar.gz"; \
		soksak-sdk pack-target --root "$(CURDIR)" --spec-root "$$tooling_root/.dependencies/soksak-spec" --target '$(TARGET)' --source "$$package" --out "$$archive"; \
		soksak-sdk package --root "$(CURDIR)" --spec-root "$$tooling_root/.dependencies/soksak-spec" --commit "$$(git rev-parse --verify HEAD)" --artifacts "$$artifacts" --target '$(TARGET)' --out "$(OUT)"

attest: require-tooling require-out release
	@tool="$$(command -v soksak-sdk)"; tooling_root="$$(cd "$$(dirname "$$tool")/.." && pwd -P)"; \
		platform="$$(node -p 'process.platform')"; architecture="$$(node -p 'process.arch')"; \
		soksak-sdk attest --release-dir "$(OUT)" --spec-root "$$tooling_root/.dependencies/soksak-spec" --tooling-release "$$tooling_root/release.json" \
		--mode native --platform "$$platform" --architecture "$$architecture" \
		--tool "go=$$(go env GOVERSION | sed 's/^go//')" --tool "node=$$(node -p 'process.versions.node')"
