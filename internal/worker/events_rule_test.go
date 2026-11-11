package worker

import (
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"strings"
	"testing"
)

// TestOnlyFinalizeWritesPageEvents enforces the MVP spec rule "page_events:
// written only by finalize" (issue #19, Data model). The query methods are
// found by reading dbgen: every method whose SQL inserts into page_events.
func TestOnlyFinalizeWritesPageEvents(t *testing.T) {
	methods := pageEventWriters(t)
	if len(methods) == 0 {
		t.Fatal("found no dbgen query that inserts into page_events; the test lost its target")
	}

	fset := token.NewFileSet()
	pkgs, err := parser.ParseDir(fset, ".", nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	for _, pkg := range pkgs {
		for path, file := range pkg.Files {
			if filepath.Base(path) == "finalize.go" || strings.HasSuffix(path, "_test.go") {
				continue
			}
			ast.Inspect(file, func(n ast.Node) bool {
				sel, ok := n.(*ast.SelectorExpr)
				if ok && methods[sel.Sel.Name] {
					t.Errorf("%s: %s writes page_events outside finalize.go; spec rule: page_events are written only by finalize (a cancelled Check records only new and reactivated, and lanes never record events)",
						fset.Position(sel.Pos()), sel.Sel.Name)
				}
				return true
			})
		}
	}
}

// pageEventWriters returns the Queries methods whose SQL constant inserts into
// page_events. sqlc names the constant after the method with a lower-case
// first letter.
func pageEventWriters(t *testing.T) map[string]bool {
	t.Helper()
	fset := token.NewFileSet()
	pkgs, err := parser.ParseDir(fset, "../dbgen", nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]bool{}
	for _, pkg := range pkgs {
		for _, file := range pkg.Files {
			ast.Inspect(file, func(n ast.Node) bool {
				vs, ok := n.(*ast.ValueSpec)
				if !ok || len(vs.Names) != 1 || len(vs.Values) != 1 {
					return true
				}
				lit, ok := vs.Values[0].(*ast.BasicLit)
				if !ok || lit.Kind != token.STRING || !strings.Contains(strings.ToLower(lit.Value), "insert into page_events") {
					return true
				}
				name := vs.Names[0].Name
				out[strings.ToUpper(name[:1])+name[1:]] = true
				return true
			})
		}
	}
	return out
}
