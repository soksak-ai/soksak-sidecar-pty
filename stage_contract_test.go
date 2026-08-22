package main

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
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
		"x86_64-unknown-linux-gnu",
		"x86_64-pc-windows-msvc",
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

func TestStageUsesTheDeclaredBuildDirectory(t *testing.T) {
	script, err := os.ReadFile("stage.sh")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(script), "${SOKSAK_BUILD_DIR:-target}") {
		t.Fatal("stage.sh must write build output under SOKSAK_BUILD_DIR")
	}
}

func TestStagePreservesTheWindowsExecutableExtension(t *testing.T) {
	repository, err := filepath.Abs(".")
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	bin := filepath.Join(root, "bin")
	if err := os.MkdirAll(bin, 0o700); err != nil {
		t.Fatal(err)
	}
	command := "#!/bin/sh\nset -eu\nwhile [ $# -gt 0 ]; do if [ \"$1\" = -o ]; then shift; mkdir -p \"$(dirname \"$1\")\"; printf binary > \"$1\"; exit 0; fi; shift; done\nexit 1\n"
	if err := os.WriteFile(filepath.Join(bin, "go"), []byte(command), 0o700); err != nil {
		t.Fatal(err)
	}
	dist := filepath.Join(root, "dist")
	shell, err := exec.LookPath("sh")
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(shell, filepath.Join(repository, "stage.sh"), dist, "x86_64-pc-windows-msvc")
	cmd.Env = append(os.Environ(), "PATH="+bin+":"+os.Getenv("PATH"), "SOKSAK_BUILD_DIR="+filepath.Join(root, "build"))
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
	root := t.TempDir()
	bin := filepath.Join(root, "bin")
	if err := os.MkdirAll(bin, 0o700); err != nil {
		t.Fatal(err)
	}
	log := filepath.Join(root, "go.log")
	lines := []string{
		"#!/bin/sh", "set -eu",
		"printf '%s %s\\n' \"$GOOS\" \"$GOARCH\" > \"$SOKSAK_TEST_GO_LOG\"",
		"while [ $# -gt 0 ]; do",
		"  if [ \"$1\" = -o ]; then shift; mkdir -p \"$(dirname \"$1\")\"; printf binary > \"$1\"; exit 0; fi",
		"  shift",
		"done",
		"exit 1",
	}
	if err := os.WriteFile(filepath.Join(bin, "go"), []byte(strings.Join(lines, "\n")+"\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	dist := filepath.Join(root, "dist")
	cmd := exec.Command("/bin/sh", "./stage.sh", dist, "aarch64-apple-darwin")
	cmd.Env = append(os.Environ(), "PATH="+bin+":"+os.Getenv("PATH"), "SOKSAK_BUILD_DIR="+filepath.Join(root, "build"), "SOKSAK_TEST_GO_LOG="+log)
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("stage: %v\n%s", err, output)
	}
	body, err := os.ReadFile(log)
	if err != nil {
		t.Fatal(err)
	}
	if string(body) != "darwin arm64\n" {
		t.Fatalf("target=%q", body)
	}
	if info, err := os.Stat(filepath.Join(dist, "soksak-sidecar-pty")); err != nil || !info.Mode().IsRegular() {
		t.Fatalf("staged info=%v err=%v", info, err)
	}
}

func TestStageBuildsTheOwnerRepositoryFromAnyWorkingDirectory(t *testing.T) {
	repository, err := filepath.Abs(".")
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	bin := filepath.Join(root, "bin")
	if err := os.MkdirAll(bin, 0o700); err != nil {
		t.Fatal(err)
	}
	log := filepath.Join(root, "go.log")
	command := "#!/bin/sh\nset -eu\nprintf '%s\n' \"$@\" > \"$SOKSAK_TEST_GO_LOG\"\nwhile [ $# -gt 0 ]; do if [ \"$1\" = -o ]; then shift; mkdir -p \"$(dirname \"$1\")\"; printf binary > \"$1\"; exit 0; fi; shift; done\nexit 1\n"
	if err := os.WriteFile(filepath.Join(bin, "go"), []byte(command), 0o700); err != nil {
		t.Fatal(err)
	}
	dist := filepath.Join(root, "dist")
	shell, err := exec.LookPath("sh")
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(shell, filepath.Join(repository, "stage.sh"), dist, "aarch64-unknown-linux-gnu")
	cmd.Dir = root
	cmd.Env = append(os.Environ(), "PATH="+bin+":"+os.Getenv("PATH"), "SOKSAK_BUILD_DIR="+filepath.Join(root, "build"), "SOKSAK_TEST_GO_LOG="+log)
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("stage: %v\n%s", err, output)
	}
	body, err := os.ReadFile(log)
	if err != nil {
		t.Fatal(err)
	}
	arguments := strings.Split(strings.TrimSpace(string(body)), "\n")
	if len(arguments) < 4 || arguments[0] != "-C" || canonicalShellPath(arguments[1]) != filepath.Clean(repository) || arguments[2] != "build" || arguments[len(arguments)-1] != "." {
		t.Fatalf("go arguments do not build from owner repository %q: %q", repository, body)
	}
}

func canonicalShellPath(value string) string {
	if runtime.GOOS == "windows" && len(value) >= 3 && value[0] == '/' && value[2] == '/' {
		value = strings.ToUpper(value[1:2]) + ":" + value[2:]
	}
	return filepath.Clean(filepath.FromSlash(value))
}
