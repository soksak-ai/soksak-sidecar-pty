package main

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// classificationDocument lists every field of the session struct and states whether the field is
// session state, process state, or derived. SESSION.md S3 splits the two and excludes the third
// from storage, and a store cannot be judged against a split nobody wrote down.
const classificationDocument = "SESSION-STATE.md"

var classificationRow = regexp.MustCompile(
	"^\\| `([A-Za-z0-9_]+)` \\| (session|process|derived) \\|")

func TestEverySessionFieldIsClassified(t *testing.T) {
	fields := sessionStructFields(t)
	if len(fields) == 0 {
		t.Fatal("the session struct has no fields; the parser found the wrong type")
	}

	body, err := os.ReadFile(classificationDocument)
	if err != nil {
		t.Fatalf("read %s: %v", classificationDocument, err)
	}

	classified := map[string]string{}
	for _, line := range strings.Split(string(body), "\n") {
		match := classificationRow.FindStringSubmatch(line)
		if match == nil {
			continue
		}
		if previous, repeated := classified[match[1]]; repeated {
			t.Errorf("%s classifies %s twice: %s and %s",
				classificationDocument, match[1], previous, match[2])
		}
		classified[match[1]] = match[2]
	}

	var missing []string
	for _, field := range fields {
		if _, ok := classified[field]; !ok {
			missing = append(missing, field)
		}
	}
	sort.Strings(missing)
	if len(missing) > 0 {
		t.Errorf("%s classifies neither session state nor process state for %s",
			classificationDocument, strings.Join(missing, ", "))
	}

	held := map[string]bool{}
	for _, field := range fields {
		held[field] = true
	}
	for field := range classified {
		if !held[field] {
			t.Errorf("%s classifies %s, which the session struct does not hold",
				classificationDocument, field)
		}
	}
}

// sessionStructFields returns the name of every field the session struct holds, embedded fields
// included by their type name.
func sessionStructFields(t *testing.T) []string {
	t.Helper()

	file, err := parser.ParseFile(token.NewFileSet(), "session.go", nil, 0)
	if err != nil {
		t.Fatalf("parse session.go: %v", err)
	}

	var fields []string
	ast.Inspect(file, func(node ast.Node) bool {
		spec, ok := node.(*ast.TypeSpec)
		if !ok || spec.Name.Name != "session" {
			return true
		}
		structure, ok := spec.Type.(*ast.StructType)
		if !ok {
			return false
		}
		for _, field := range structure.Fields.List {
			if len(field.Names) == 0 {
				if named, ok := field.Type.(*ast.Ident); ok {
					fields = append(fields, named.Name)
				}
				continue
			}
			for _, name := range field.Names {
				fields = append(fields, name.Name)
			}
		}
		return false
	})
	return fields
}
