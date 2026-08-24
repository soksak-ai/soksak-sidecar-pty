package main

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestReleaseTargetsContainOnlyVerifiedRuntimePlatforms(t *testing.T) {
	data, err := os.ReadFile("release/targets.json")
	if err != nil {
		t.Fatal(err)
	}
	var targets []struct {
		Target string `json:"target"`
	}
	if err := json.Unmarshal(data, &targets); err != nil {
		t.Fatal(err)
	}
	want := []string{
		"aarch64-apple-darwin",
		"aarch64-unknown-linux-gnu",
		"x86_64-apple-darwin",
		"x86_64-pc-windows-msvc",
		"x86_64-unknown-linux-gnu",
	}
	if len(targets) != len(want) {
		t.Fatalf("release targets=%v, want %v", targets, want)
	}
	for index := range want {
		if targets[index].Target != want[index] {
			t.Fatalf("release target %d=%q, want %q", index, targets[index].Target, want[index])
		}
	}
}

func TestSidecarManifestUsesCanonicalFields(t *testing.T) {
	body, err := os.ReadFile("sidecar.json")
	if err != nil {
		t.Fatal(err)
	}
	var manifest map[string]json.RawMessage
	if err := json.Unmarshal(body, &manifest); err != nil {
		t.Fatal(err)
	}
	want := map[string]bool{"id": true, "version": true, "interface": true, "process": true}
	if len(manifest) != len(want) {
		t.Fatalf("manifest fields=%v", manifest)
	}
	for field := range manifest {
		if !want[field] {
			t.Fatalf("unsupported manifest field %q", field)
		}
	}
}

func TestStageConsumesOnlyAnExplicitBuiltArtifact(t *testing.T) {
	script, err := os.ReadFile("stage.sh")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(script), "SOKSAK_BUILD_DIR") || strings.Contains(string(script), " go ") {
		t.Fatal("stage.sh must not retain the legacy build environment or compile source")
	}
	if strings.Contains(string(script), "\"version\": \"0.0.") {
		t.Fatal("stage.sh must not duplicate the sidecar version")
	}
	if !strings.Contains(string(script), "\"$repository/sidecar.json\"") {
		t.Fatal("stage.sh must derive the staged manifest from sidecar.json")
	}
}

func TestStagePreservesTheWindowsExecutableExtension(t *testing.T) {
	repository, err := filepath.Abs(".")
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	buildRoot := filepath.Join(root, "build")
	built := filepath.Join(buildRoot, "x86_64-pc-windows-msvc", "release", "soksak-sidecar-pty.exe")
	if err := os.MkdirAll(filepath.Dir(built), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(built, []byte("binary"), 0o700); err != nil {
		t.Fatal(err)
	}
	dist := filepath.Join(root, "dist")
	shell, err := exec.LookPath("sh")
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(shell, filepath.Join(repository, "stage.sh"), dist, "x86_64-pc-windows-msvc", buildRoot)
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("stage: %v\n%s", err, output)
	}
	if _, err := os.Stat(filepath.Join(dist, "soksak-sidecar-pty.exe")); err != nil {
		t.Fatal(err)
	}
	body, err := os.ReadFile(filepath.Join(dist, "sidecar.json"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(body), `"process": "dist/soksak-sidecar-pty.exe"`) {
		t.Fatalf("manifest = %s", body)
	}
}

func verifyStageDispatchesDeclaredDarwinARM64Target(t *testing.T) {
	output, err := exec.Command("/bin/sh", "./scripts/resolve-target.sh", "aarch64-apple-darwin").CombinedOutput()
	if err != nil {
		t.Fatalf("resolve target: %v\n%s", err, output)
	}
	if string(output) != "darwin arm64 none\n" {
		t.Fatalf("target mapping=%q", output)
	}
}

func TestStageReadsTheExplicitBuildRootFromAnyWorkingDirectory(t *testing.T) {
	repository, err := filepath.Abs(".")
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	buildRoot := filepath.Join(root, "build")
	built := filepath.Join(buildRoot, "aarch64-unknown-linux-gnu", "release", "soksak-sidecar-pty")
	if err := os.MkdirAll(filepath.Dir(built), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(built, []byte("binary"), 0o700); err != nil {
		t.Fatal(err)
	}
	dist := filepath.Join(root, "dist")
	shell, err := exec.LookPath("sh")
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(shell, filepath.Join(repository, "stage.sh"), dist, "aarch64-unknown-linux-gnu", buildRoot)
	cmd.Dir = root
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("stage: %v\n%s", err, output)
	}
	if body, err := os.ReadFile(filepath.Join(dist, "soksak-sidecar-pty")); err != nil || string(body) != "binary" {
		t.Fatalf("staged binary=%q err=%v", body, err)
	}
}
