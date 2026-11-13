package suite

import "testing"

func TestBuiltin(t *testing.T) {
	loaders := Builtin("testdata/valid")
	if len(loaders) != 1 {
		t.Fatalf("Builtin returned %d loaders", len(loaders))
	}
	if loaders[0].GetName() != LoaderLocal || loaders[0].GetVersion() != "1.0.0" {
		t.Fatalf("loader %s/%s", loaders[0].GetName(), loaders[0].GetVersion())
	}
}
