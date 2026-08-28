package main

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
)

func TestProjectProcessNameIsOwnedByTheManifestAndInstaller(t *testing.T) {
	body, err := os.ReadFile("sidecar.json")
	if err != nil {
		t.Fatal(err)
	}
	var manifest struct {
		ProcessRole string `json:"processRole"`
	}
	if err := json.Unmarshal(body, &manifest); err != nil {
		t.Fatal(err)
	}
	if manifest.ProcessRole != "sidecar-pty" {
		t.Fatalf("processRole=%q want sidecar-pty", manifest.ProcessRole)
	}
	for _, name := range []string{"main.go", "process_name_darwin.go"} {
		source, err := os.ReadFile(name)
		if err != nil && os.IsNotExist(err) {
			continue
		}
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(source), "setprogname") || strings.Contains(string(source), "applyPlatformProcessName") {
			t.Fatalf("%s rewrites the process name after installation", name)
		}
	}
}
