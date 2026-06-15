package dctl

import (
	"go/parser"
	"go/token"
	"os"
	"strings"
	"testing"
)

// The root dctl package must stay a pure Discord client: it may not import any
// project package (kernel, discord, internal/...). This guards the invariant
// "dctl is usable without the core".
func TestRootImportsNoDomain(t *testing.T) {
	fset := token.NewFileSet()
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".go") || strings.HasSuffix(e.Name(), "_test.go") {
			continue
		}
		f, err := parser.ParseFile(fset, e.Name(), nil, parser.ImportsOnly)
		if err != nil {
			t.Fatal(err)
		}
		for _, imp := range f.Imports {
			p := strings.Trim(imp.Path.Value, `"`)
			if strings.HasPrefix(p, "github.com/vskstudio/dctl/") {
				t.Errorf("%s imports domain package %q — root must stay a pure client", e.Name(), p)
			}
		}
	}
}
