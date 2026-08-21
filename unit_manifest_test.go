package main

import (
	"encoding/json"
	"os"
	"testing"
)

func TestUnitManifestDeclaresPTYContractAndProcess(t *testing.T) {
	body, err := os.ReadFile("soksak-unit.json")
	if err != nil {
		t.Fatal(err)
	}
	var manifest struct {
		Spec, Kind, ID, Version string
		Dependencies            []any
		Implements              []struct{ ID, Version string }
		Consumes                []any
		Entrypoints             []struct{ Role, Name, Path string }
	}
	if err := json.Unmarshal(body, &manifest); err != nil {
		t.Fatal(err)
	}
	if manifest.Spec != "soksak-spec-unit@0.0.1" || manifest.Kind != "sidecar" || manifest.ID != "soksak-sidecar-pty" || manifest.Version != "0.0.1" {
		t.Fatalf("identity=%+v", manifest)
	}
	if len(manifest.Dependencies) != 0 || len(manifest.Consumes) != 0 || len(manifest.Implements) != 1 || manifest.Implements[0].ID != "soksak-spec-sidecar-pty" || manifest.Implements[0].Version != "0.0.1" {
		t.Fatalf("contracts=%+v", manifest)
	}
	if len(manifest.Entrypoints) != 1 || manifest.Entrypoints[0].Role != "process" || manifest.Entrypoints[0].Name != "pty" || manifest.Entrypoints[0].Path != "dist/soksak-sidecar-pty" {
		t.Fatalf("entrypoints=%+v", manifest.Entrypoints)
	}
}
