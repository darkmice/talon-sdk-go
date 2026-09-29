package talon

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"strings"
	"testing"
)

// A return statement evaluates every expression before returning. Reading an
// output field beside json.Unmarshal therefore returns the old zero value.
// Keep this check across all SDK files because the same bug affected KV, AI,
// FTS, Geo, Graph, MQ, and TS methods.
func TestNoEagerJSONUnmarshalReturns(t *testing.T) {
	fset := token.NewFileSet()
	packages, err := parser.ParseDir(fset, ".", func(info os.FileInfo) bool {
		return strings.HasPrefix(info.Name(), "talon") && strings.HasSuffix(info.Name(), ".go") && !strings.HasSuffix(info.Name(), "_test.go")
	}, 0)
	if err != nil {
		t.Fatal(err)
	}
	for _, pkg := range packages {
		for _, file := range pkg.Files {
			ast.Inspect(file, func(node ast.Node) bool {
				ret, ok := node.(*ast.ReturnStmt)
				if !ok || len(ret.Results) < 2 {
					return true
				}
				for _, result := range ret.Results {
					call, ok := result.(*ast.CallExpr)
					if !ok {
						continue
					}
					selector, ok := call.Fun.(*ast.SelectorExpr)
					if !ok || selector.Sel.Name != "Unmarshal" {
						continue
					}
					object, ok := selector.X.(*ast.Ident)
					if ok && object.Name == "json" {
						t.Errorf("eager json.Unmarshal return at %s", fset.Position(ret.Pos()))
					}
				}
				return true
			})
		}
	}
}
