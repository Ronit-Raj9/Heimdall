package schemas

import (
	"go/ast"
	"go/parser"
	"go/token"
	"slices"
	"strconv"
	"strings"
	"testing"
)

// TestGenericProbeIDsComplete: GenericProbeIDs lists every generic probe
// (each resolves through GenericProbe), and every GenericProbe* constant of
// genericprobes.go is in it, so the catalog never misses a new one.
func TestGenericProbeIDsComplete(t *testing.T) {
	if len(GenericProbeIDs) == 0 {
		t.Fatal("GenericProbeIDs is empty")
	}
	seen := map[string]bool{}
	for _, id := range GenericProbeIDs {
		if seen[id] {
			t.Errorf("%s listed twice", id)
		}
		seen[id] = true
		p := GenericProbe(id)
		if p == nil {
			t.Errorf("GenericProbe(%q) = nil", id)
			continue
		}
		if p.ID != id || p.Layer == "" {
			t.Errorf("GenericProbe(%q) = id %q layer %q", id, p.ID, p.Layer)
		}
	}
	f, err := parser.ParseFile(token.NewFileSet(), "genericprobes.go", nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	var consts []string
	ast.Inspect(f, func(n ast.Node) bool {
		vs, ok := n.(*ast.ValueSpec)
		if !ok {
			return true
		}
		for i, name := range vs.Names {
			if !strings.HasPrefix(name.Name, "GenericProbe") || i >= len(vs.Values) {
				continue
			}
			if lit, ok := vs.Values[i].(*ast.BasicLit); ok && lit.Kind == token.STRING {
				v, _ := strconv.Unquote(lit.Value)
				consts = append(consts, v)
			}
		}
		return true
	})
	if len(consts) == 0 {
		t.Fatal("found no GenericProbe constants")
	}
	for _, id := range consts {
		if !slices.Contains(GenericProbeIDs, id) {
			t.Errorf("constant %q is missing from GenericProbeIDs", id)
		}
	}
	if len(consts) != len(GenericProbeIDs) {
		t.Errorf("%d constants, %d ids", len(consts), len(GenericProbeIDs))
	}
}
