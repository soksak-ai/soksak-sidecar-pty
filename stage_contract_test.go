package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestStageUsesTheDeclaredBuildDirectory(t *testing.T) {
	script, err := os.ReadFile("stage.sh")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(script), "${SOKSAK_BUILD_DIR:-target}") {
		t.Fatal("stage.sh must write build output under SOKSAK_BUILD_DIR")
	}
}

func TestStageDispatchesDeclaredDarwinARM64Target(t *testing.T) {
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
