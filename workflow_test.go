package main

import (
	"os"
	"strings"
	"testing"
)

func TestNativeWorkflowRunsWindowsAndLinuxPTYTests(t *testing.T) {
	body, err := os.ReadFile(".github/workflows/verify.yml")
	if err != nil {
		t.Fatal(err)
	}
	source := string(body)
	for _, required := range []string{
		"windows-2025", "ubuntu-24.04", "go test -count=1 ./...", "go vet ./...",
	} {
		if !strings.Contains(source, required) {
			t.Errorf("native workflow is missing %s", required)
		}
	}
}

func TestEveryWorkflowUsesNode24CompatibleActions(t *testing.T) {
	entries, err := os.ReadDir(".github/workflows")
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".yml") {
			continue
		}
		body, err := os.ReadFile(".github/workflows/" + entry.Name())
		if err != nil {
			t.Fatal(err)
		}
		source := string(body)
		for _, obsolete := range []string{"actions/checkout@11bd719", "actions/setup-go@40f1582", "actions/upload-artifact@ea165f8"} {
			if strings.Contains(source, obsolete) {
				t.Errorf("%s uses obsolete Action %s", entry.Name(), obsolete)
			}
		}
	}
}

func TestReleaseWorkflowUsesTheCanonicalImmutablePublisher(t *testing.T) {
	body, err := os.ReadFile(".github/workflows/release.yml")
	if err != nil {
		t.Fatal(err)
	}
	source := string(body)
	for _, required := range []string{
		"aarch64-apple-darwin", "aarch64-unknown-linux-gnu",
		"x86_64-apple-darwin", "x86_64-unknown-linux-gnu",
		"x86_64-pc-windows-msvc",
		"https://github.com/soksak-ai/soksak-spec/releases/download/v0.0.29/soksak-ai-plugin-spec-0.0.29.tgz",
		"f3311f6e20069667486e753e8669e7f7787f197f47e4a94e14207a066c2db32c",
		"node-version-file: .dependency/spec-package/package.json",
		"--spec-package .dependency/spec-package",
		"require(\"./sidecar.json\").version",
		"release-template/sidecar/build-release.mjs",
		"release-template/sidecar/validate-with-spec.mjs",
		"release-template/publish-canonical-release.mjs",
		"GH_TOKEN: ${{ steps.release-token.outputs.token }}",
		"owner-enforced immutable releases must be enabled",
		"go-version-file: go.mod",
	} {
		if !strings.Contains(source, required) {
			t.Errorf("release workflow is missing %s", required)
		}
	}
	if strings.Contains(source, "gh release create") {
		t.Error("release workflow creates protected tags implicitly")
	}
	if strings.Contains(source, "repository: soksak-ai/soksak-spec") || strings.Contains(source, "pnpm/action-setup") {
		t.Error("release workflow rebuilds the spec source instead of using its immutable package")
	}
}

func TestWindowsSystemArtifactRunsConPTYBeforePackaging(t *testing.T) {
	body, err := os.ReadFile(".github/workflows/windows-system-artifact.yml")
	if err != nil {
		t.Fatal(err)
	}
	source := string(body)
	for _, required := range []string{
		"windows-2025", "go test -count=1 ./...", "go vet ./...",
		"x86_64-pc-windows-msvc", "windows-system-artifact", "soksak-sidecar-pty.exe",
	} {
		if !strings.Contains(source, required) {
			t.Errorf("Windows system artifact workflow is missing %s", required)
		}
	}
}

func TestWindowsListenerUsesTheNamedPipeTransport(t *testing.T) {
	body, err := os.ReadFile("listen_windows.go")
	if err != nil {
		t.Fatal(err)
	}
	source := string(body)
	for _, required := range []string{"winio.ListenPipe", "SecurityDescriptor", "GetCurrentProcessToken"} {
		if !strings.Contains(source, required) {
			t.Errorf("Windows listener is missing %s", required)
		}
	}
	mainSource, err := os.ReadFile("main.go")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Count(string(mainSource), "ControlSocketPath(runtimeRoot") != 1 {
		t.Fatal("PTY daemon derives its control address more than once")
	}
}
