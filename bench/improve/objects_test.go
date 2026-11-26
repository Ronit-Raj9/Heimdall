package improve

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestObjectsPutGetHas(t *testing.T) {
	o := Objects{Root: t.TempDir()}
	b := BundleObject{Files: map[string]string{"/home/agent/AGENTS.md": "hello {{x}}"}, Raw: []string{"/home/agent/AGENTS.md"}}
	h, err := o.Put(ObjBundle, b)
	if err != nil {
		t.Fatal(err)
	}
	if len(h) != 64 {
		t.Fatalf("hash %q", h)
	}
	p := filepath.Join(o.Root, "sha256", h[:2], h+".json")
	st, err := os.Stat(p)
	if err != nil {
		t.Fatal(err)
	}
	if st.Mode().Perm() != 0o644 {
		t.Errorf("mode %v", st.Mode().Perm())
	}
	if !o.Has(h) {
		t.Error("Has false after Put")
	}
	// Writing the same value again gives the same hash and no error.
	h2, err := o.Put(ObjBundle, b)
	if err != nil || h2 != h {
		t.Fatalf("second put: %q %v", h2, err)
	}
	var got BundleObject
	if err := o.Get(h, ObjBundle, &got); err != nil {
		t.Fatal(err)
	}
	if got.Files["/home/agent/AGENTS.md"] != "hello {{x}}" || len(got.Raw) != 1 {
		t.Fatalf("got %+v", got)
	}
	// The kind is part of the object: the same value as another kind hashes differently.
	h3, err := o.Put(ObjPlaybook, b)
	if err != nil || h3 == h {
		t.Fatalf("kind not in hash: %q %v", h3, err)
	}
	if err := o.Get(h, ObjPlaybook, &got); err == nil {
		t.Error("Get accepted the wrong kind")
	}
}

func TestObjectsMissingAndMismatch(t *testing.T) {
	o := Objects{Root: t.TempDir()}
	missing := "abcdef0123456789abcdef0123456789abcdef0123456789abcdef0123456789"
	var v MeasureObject
	if err := o.Get(missing, ObjMeasure, &v); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing: %v", err)
	}
	if o.Has(missing) {
		t.Error("Has true for a missing object")
	}
	h, err := o.Put(ObjMeasure, MeasureObject{Phase: "audit", Round: 1, TasksHash: "x", NTasks: 3})
	if err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(o.Root, "sha256", h[:2], h+".json")
	if err := os.WriteFile(p, []byte(`{"kind":"measure","v":{"phase":"sealed"}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := o.Put(ObjMeasure, MeasureObject{Phase: "audit", Round: 1, TasksHash: "x", NTasks: 3}); !errors.Is(err, ErrObjectMismatch) {
		t.Fatalf("put over a changed file: %v", err)
	}
	if err := o.Get(h, ObjMeasure, &v); !errors.Is(err, ErrObjectMismatch) {
		t.Fatalf("get of a changed file: %v", err)
	}
	for _, bad := range []string{"", "../x", "ZZ", h[:10]} {
		if err := o.Get(bad, ObjMeasure, &v); err == nil {
			t.Errorf("Get accepted hash %q", bad)
		}
	}
}
