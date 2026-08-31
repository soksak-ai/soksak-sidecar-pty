package main

import (
	"encoding/json"
	"os"
	"strings"
	"testing"

	ptycontract "github.com/soksak-ai/soksak-contract-pty"
)

func TestProjectProcessNameIsOwnedByTheManifestAndInstaller(t *testing.T) {
	body, err := os.ReadFile("sidecar.json")
	if err != nil {
		t.Fatal(err)
	}
	var manifest struct {
		ID          string `json:"id"`
		ProcessRole string `json:"processRole"`
		Interface   []struct {
			ID      string `json:"id"`
			Version string `json:"version"`
		} `json:"interface"`
	}
	if err := json.Unmarshal(body, &manifest); err != nil {
		t.Fatal(err)
	}
	if manifest.ProcessRole != "sidecar-pty" {
		t.Fatalf("processRole=%q want sidecar-pty", manifest.ProcessRole)
	}
	if manifest.ID != componentID {
		t.Fatalf("id=%q want implementation-owned %q", manifest.ID, componentID)
	}
	if len(manifest.Interface) != 1 || manifest.Interface[0].ID != ptycontract.InterfaceID || manifest.Interface[0].Version != ptycontract.InterfaceVersion {
		t.Fatalf("interface=%+v want %s@%s", manifest.Interface, ptycontract.InterfaceID, ptycontract.InterfaceVersion)
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
