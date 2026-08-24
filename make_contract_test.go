package main

import (
	"os"
	"strings"
	"testing"
)

func TestMakeOwnsEveryPTYBuildEntrypoint(t *testing.T) {
	body, err := os.ReadFile("Makefile")
	if err != nil {
		t.Fatal(err)
	}
	source := string(body)
	for _, target := range []string{"preflight:", "prepare:", "build:", "stage:", "verify:"} {
		if !strings.Contains(source, target) {
			t.Errorf("Makefile omits %s", target)
		}
	}
	for _, target := range []string{
		"aarch64-apple-darwin", "x86_64-apple-darwin",
		"aarch64-unknown-linux-gnu", "x86_64-unknown-linux-gnu",
		"x86_64-pc-windows-msvc",
	} {
		if !strings.Contains(source, target) {
			t.Errorf("Makefile omits %s", target)
		}
	}
	for _, duplicate := range []string{"GO_VERSION :=", "NODE_VERSION :=", "PNPM_VERSION :="} {
		if strings.Contains(source, duplicate) {
			t.Errorf("Makefile duplicates ecosystem metadata: %s", duplicate)
		}
	}
}

func TestPTYWorkflowsProjectOwnerMetadataIntoMake(t *testing.T) {
	release := readWorkflow(t, ".github/workflows/release.yml")
	for _, required := range []string{
		"spec_url:", "spec_sha256:", "go-version-file: go.mod",
		"make verify TARGET=\"${{ matrix.target }}\"",
		"make stage TARGET=\"${{ matrix.target }}\" OUT=dist",
	} {
		if !strings.Contains(release, required) {
			t.Errorf("release workflow omits %s", required)
		}
	}
	for _, forbidden := range []string{
		"go-version: \"1.25.0\"", "./stage.sh dist", "go test -count=1 ./... && go vet ./...",
	} {
		if strings.Contains(release, forbidden) {
			t.Errorf("release workflow bypasses owner metadata or Make through %s", forbidden)
		}
	}

	verify := readWorkflow(t, ".github/workflows/verify.yml")
	for _, required := range []string{"go-version-file: go.mod", "make verify TARGET=\"${{ matrix.target }}\""} {
		if !strings.Contains(verify, required) {
			t.Errorf("verify workflow omits %s", required)
		}
	}
	if strings.Contains(verify, "go-version: \"1.25.0\"") {
		t.Error("verify workflow duplicates the Go version from go.mod")
	}

	diagnostic := readWorkflow(t, ".github/workflows/conpty-compare.yml")
	if !strings.Contains(diagnostic, "go-version-file: diagnostics/conptycompare/go.mod") || strings.Contains(diagnostic, "go-version: \"1.25.0\"") {
		t.Error("ConPTY diagnostic workflow does not use its module-owned Go version")
	}
}

func readWorkflow(t *testing.T, path string) string {
	t.Helper()
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(body)
}
