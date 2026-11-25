package main

import (
	"go/parser"
	"go/token"
	"os"
	"strconv"
	"strings"
	"testing"
)

// The API only serves requests. Periodic work (the storage lifecycle sweeps,
// dispatch, recovery) runs in the orchestrator (cmd/worker), so running
// several API instances does not multiply it.
func TestAPIRunsNoPeriodicWork(t *testing.T) {
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		name := e.Name()
		if !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		f, err := parser.ParseFile(token.NewFileSet(), name, nil, parser.ImportsOnly)
		if err != nil {
			t.Fatal(err)
		}
		for _, imp := range f.Imports {
			path, _ := strconv.Unquote(imp.Path.Value)
			for _, banned := range []string{"/services/lifecycle", "/services/orchestrator"} {
				if strings.HasSuffix(path, banned) {
					t.Errorf("%s imports %s; periodic work belongs in cmd/worker", name, path)
				}
			}
		}
	}
}
