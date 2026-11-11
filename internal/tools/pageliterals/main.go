// Command pageliterals fails when Go code outside internal/page, internal/dbgen
// and _test.go files spells a Page state, origin or event kind as a string
// literal. Use the typed constants in internal/page instead. A literal that
// only shares its spelling (a Check trigger, a schedule, an HTML id) is exempt
// with a same-line comment containing "pageliterals:ok" and the reason.
//
// Run from the module root: go run ./internal/tools/pageliterals
package main

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/ihorbryk/seo-site-checker/internal/page"
)

// banned maps each Page constant's value to its name.
var banned = map[string]string{
	page.StateActive: "page.StateActive", page.StateRetired: "page.StateRetired (or page.EventRetired)", page.StateIgnored: "page.StateIgnored",
	page.OriginSeed: "page.OriginSeed", page.OriginDiscovered: "page.OriginDiscovered", page.OriginManual: "page.OriginManual",
	page.EventNew: "page.EventNew", page.EventVanished: "page.EventVanished", page.EventReactivated: "page.EventReactivated",
}

const allow = "pageliterals:ok"

func main() {
	fset := token.NewFileSet()
	bad := 0
	err := filepath.WalkDir(".", func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		slash := filepath.ToSlash(path)
		if d.IsDir() {
			if name := d.Name(); name != "." && strings.HasPrefix(name, ".") ||
				slash == "internal/page" || slash == "internal/dbgen" {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		file, err := parser.ParseFile(fset, path, nil, parser.ParseComments)
		if err != nil {
			return err
		}
		allowed := map[int]bool{}
		for _, cg := range file.Comments {
			for _, c := range cg.List {
				if strings.Contains(c.Text, allow) {
					allowed[fset.Position(c.Pos()).Line] = true
				}
			}
		}
		ast.Inspect(file, func(n ast.Node) bool {
			lit, ok := n.(*ast.BasicLit)
			if !ok || lit.Kind != token.STRING || allowed[fset.Position(lit.Pos()).Line] {
				return true
			}
			if v, err := strconv.Unquote(lit.Value); err == nil {
				if use, hit := banned[v]; hit {
					fmt.Printf("%s: string literal %s; use %s\n", fset.Position(lit.Pos()), lit.Value, use)
					bad++
				}
			}
			return true
		})
		return nil
	})
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}
	if bad > 0 {
		fmt.Printf("%d Page state/origin/event literals outside internal/page (use internal/page constants, or mark a same-spelling literal with // %s <reason>)\n", bad, allow)
		os.Exit(1)
	}
}
