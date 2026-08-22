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
		"ref: fb6f53066b835e8a641f34d4aab8c4248d0f261d",
		"release-template/sidecar/build-release.mjs",
		"release-template/sidecar/validate-with-spec.mjs",
		"release-template/publish-canonical-release.mjs",
		"GH_TOKEN: ${{ steps.release-token.outputs.token }}",
		"owner-enforced immutable releases must be enabled",
	} {
		if !strings.Contains(source, required) {
			t.Errorf("release workflow is missing %s", required)
		}
	}
	if strings.Contains(source, "gh release create") {
		t.Error("release workflow creates protected tags implicitly")
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
