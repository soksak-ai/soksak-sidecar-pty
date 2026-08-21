package main

import (
	"os"
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
