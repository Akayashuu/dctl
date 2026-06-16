package dctl

import (
	"go/parser"
	"go/token"
	"os"
	"strings"
	"testing"
)

// The root dctl package must stay a pure, dependency-free Discord client: it may
// import only the standard library and internal/transport. Any third-party module
// or other internal package is a regression. This guards the invariant that dctl
// builds standalone with an empty go.mod require block.
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
			if p == "github.com/Herrscherd/dctl/internal/transport" {
				continue
			}
			// Stdlib import paths have no dot in their first segment.
			if strings.Contains(strings.SplitN(p, "/", 2)[0], ".") {
				t.Errorf("%s imports non-stdlib package %q — root must stay a pure, dependency-free client", e.Name(), p)
			}
		}
	}
}
