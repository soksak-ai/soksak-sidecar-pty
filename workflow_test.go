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
