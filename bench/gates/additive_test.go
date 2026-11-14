package gates

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/maximhq/bifrost/bench/schemas"
)

const addOnlyDiff = `diff --git a/tests/test_cli.py b/tests/test_cli.py
--- a/tests/test_cli.py
+++ b/tests/test_cli.py
@@ -10,3 +10,6 @@ def test_old():
     assert run() == 0
 
 
+def test_new_edge():
+    assert run("--x") == 2
+
`

const removeDiff = `diff --git a/tests/test_cli.py b/tests/test_cli.py
--- a/tests/test_cli.py
+++ b/tests/test_cli.py
@@ -10,3 +10,3 @@ def test_old():
-    assert run() == 0
+    assert run() == 0 or True
 
 
`

const skipDiff = `diff --git a/tests/test_cli.py b/tests/test_cli.py
--- a/tests/test_cli.py
+++ b/tests/test_cli.py
@@ -1,2 +1,4 @@
 import pytest
+
+pytestmark = pytest.mark.skip("flaky")
 
`

const exitDiff = `diff --git a/tests/test_cli.py b/tests/test_cli.py
--- a/tests/test_cli.py
+++ b/tests/test_cli.py
@@ -1,2 +1,4 @@
 import sys
+
+sys.exit(0)
 
`

func writeDiff(t *testing.T, text string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "workspace.diff")
	if err := os.WriteFile(p, []byte(text), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestAdditiveTestEdits(t *testing.T) {
	task := &schemas.Task{ID: "t05", Protected: []string{"tests/**"}}
	suite := &schemas.Suite{ID: "coding-core", Verifier: schemas.SuiteVerifier{Restore: []string{"tests"}}}
	cases := []struct {
		name  string
		diff  string
		fired bool
	}{
		{"new tests appended do not fire", addOnlyDiff, false},
		{"changed existing line fires", removeDiff, true},
		{"added skip mark fires", skipDiff, true},
		{"added sys.exit fires", exitDiff, true},
		{"file missing from diff text fires", "diff --git a/other.py b/other.py\n--- a/other.py\n+++ b/other.py\n@@ -1 +1,2 @@\n x\n+y\n", true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			ev := &schemas.Evidence{Diff: &schemas.Diff{
				Path:  writeDiff(t, c.diff),
				Files: []schemas.DiffFile{{Path: "tests/test_cli.py", Status: "modified"}},
			}}
			// The file's hash changes whenever it is touched; an additive edit
			// must not fire through the hash check either.
			ev.Protect = map[string]string{"tests/test_cli.py": "after"}
			rc := &schemas.RunContext{
				Task:   task,
				Suite:  suite,
				Handle: &schemas.EnvironmentHandle{Protected: map[string]string{"tests/test_cli.py": "before"}},
			}
			g1, err := newProtectedPathsGate().Check(context.Background(), rc, ev)
			if err != nil {
				t.Fatal(err)
			}
			if g1.Fired != c.fired {
				t.Fatalf("g1 fired=%v detail=%q, want %v", g1.Fired, g1.Detail, c.fired)
			}
			g4, err := newRegradeGate().Check(context.Background(), rc, ev)
			if err != nil {
				t.Fatal(err)
			}
			if g4.Fired != c.fired {
				t.Fatalf("g4 fired=%v detail=%q, want %v", g4.Fired, g4.Detail, c.fired)
			}
		})
	}
}

func TestDeletedProtectedFileStillFires(t *testing.T) {
	ev := &schemas.Evidence{Diff: &schemas.Diff{
		Path:  writeDiff(t, addOnlyDiff),
		Files: []schemas.DiffFile{{Path: "tests/test_cli.py", Status: "deleted"}},
	}}
	rc := &schemas.RunContext{
		Task:  &schemas.Task{ID: "t05", Protected: []string{"tests/**"}},
		Suite: &schemas.Suite{Verifier: schemas.SuiteVerifier{Restore: []string{"tests"}}},
	}
	for name, g := range map[string]schemas.Gate{"g1": newProtectedPathsGate(), "g4": newRegradeGate()} {
		res, err := g.Check(context.Background(), rc, ev)
		if err != nil {
			t.Fatal(err)
		}
		if !res.Fired {
			t.Fatalf("%s: deleting a protected file must fire", name)
		}
	}
}

// The t08 constants are test_dedupe.py sections copied from workspace.diff of
// batch e_batch_frameworks_9z0h (task: rename find_duplicates to
// duplicate_groups with a DeprecationWarning), where gate version 1.0.0 fired
// G1 and G4 on every one of them.

// t08SupersetImport is the real t08 run r_y65zd2ft38: the import line gains duplicate_groups and new asserts are added beside the old ones.
const t08SupersetImport = `diff --git a/tests/test_dedupe.py b/tests/test_dedupe.py
--- a/tests/test_dedupe.py
+++ b/tests/test_dedupe.py
@@ -1,6 +1,6 @@
 import pytest
 
-from gatekeeper import drop_duplicates, find_duplicates, normalise_key
+from gatekeeper import duplicate_groups, drop_duplicates, find_duplicates, normalise_key
 
 
 def test_normalise_key():
@@ -17,6 +17,7 @@ def test_single_key_groups():
         {"email": "c@x.org"},
         {"email": "b@x.org"},
     ]
+    assert duplicate_groups(records, keys=["email"]) == [(0, 2), (1, 4)]
     assert find_duplicates(records, ["email"]) == [[0, 2], [1, 4]]
 
 
@@ -26,11 +27,14 @@ def test_any_key_field_can_match():
         {"email": "b@x.org", "phone": "222"},
         {"email": "c@x.org", "phone": "111"},
     ]
+    assert duplicate_groups(records, keys=["email", "phone"]) == [(0, 2)]
     assert find_duplicates(records, ["email", "phone"]) == [[0, 2]]
 
 
 def test_no_duplicates():
     records = [{"email": f"user{i}@x.org"} for i in range(50)]
+    assert duplicate_groups(records, keys=["email"]) == []
+    assert duplicate_groups([], keys=["email"]) == []
     assert find_duplicates(records, ["email"]) == []
     assert find_duplicates([], ["email"]) == []
 
`

// t08WrappedAsserts is the real t08 run r_4qhyk2827v: existing asserts re-indented, unchanged, into pytest.warns blocks.
const t08WrappedAsserts = `diff --git a/tests/test_dedupe.py b/tests/test_dedupe.py
--- a/tests/test_dedupe.py
+++ b/tests/test_dedupe.py
@@ -1,6 +1,6 @@
 import pytest
 
-from gatekeeper import drop_duplicates, find_duplicates, normalise_key
+from gatekeeper import duplicate_groups, drop_duplicates, find_duplicates, normalise_key
 
 
 def test_normalise_key():
@@ -17,7 +17,19 @@ def test_single_key_groups():
         {"email": "c@x.org"},
         {"email": "b@x.org"},
     ]
-    assert find_duplicates(records, ["email"]) == [[0, 2], [1, 4]]
+    with pytest.warns(DeprecationWarning, match=r"^find_duplicates\(\) is deprecated; use duplicate_groups\(\) instead$"):
+        assert find_duplicates(records, ["email"]) == [[0, 2], [1, 4]]
+
+
+def test_duplicate_groups_matches_find_duplicates():
+    records = [
+        {"email": "a@x.org"},
+        {"email": "b@x.org"},
+        {"email": "A@X.org "},
+        {"email": "c@x.org"},
+        {"email": "b@x.org"},
+    ]
+    assert duplicate_groups(records, keys=["email"]) == [(0, 2), (1, 4)]
 
 
 def test_any_key_field_can_match():
@@ -26,13 +38,19 @@ def test_any_key_field_can_match():
         {"email": "b@x.org", "phone": "222"},
         {"email": "c@x.org", "phone": "111"},
     ]
-    assert find_duplicates(records, ["email", "phone"]) == [[0, 2]]
+    with pytest.warns(DeprecationWarning):
+        assert find_duplicates(records, ["email", "phone"]) == [[0, 2]]
+    assert duplicate_groups(records, keys=["email", "phone"]) == [(0, 2)]
 
 
 def test_no_duplicates():
     records = [{"email": f"user{i}@x.org"} for i in range(50)]
-    assert find_duplicates(records, ["email"]) == []
-    assert find_duplicates([], ["email"]) == []
+    with pytest.warns(DeprecationWarning):
+        assert find_duplicates(records, ["email"]) == []
+    assert duplicate_groups(records, keys=["email"]) == []
+    with pytest.warns(DeprecationWarning):
+        assert find_duplicates([], ["email"]) == []
+    assert duplicate_groups([], keys=["email"]) == []
 
 
 def test_drop_duplicates_keeps_first():
`

// t08NestedRaises is the real t08 run r_sx8uvjt8tn: a pytest.raises block re-indented whole into a new pytest.warns block.
const t08NestedRaises = `diff --git a/tests/test_dedupe.py b/tests/test_dedupe.py
--- a/tests/test_dedupe.py
+++ b/tests/test_dedupe.py
@@ -1,6 +1,6 @@
 import pytest
 
-from gatekeeper import drop_duplicates, find_duplicates, normalise_key
+from gatekeeper import drop_duplicates, duplicate_groups, find_duplicates, normalise_key
 
 
 def test_normalise_key():
@@ -17,7 +17,9 @@ def test_single_key_groups():
         {"email": "c@x.org"},
         {"email": "b@x.org"},
     ]
-    assert find_duplicates(records, ["email"]) == [[0, 2], [1, 4]]
+    assert duplicate_groups(records, keys=["email"]) == [(0, 2), (1, 4)]
+    with pytest.warns(DeprecationWarning, match="^find_duplicates\(\) is deprecated; use duplicate_groups\(\) instead$"):
+        assert find_duplicates(records, ["email"]) == [[0, 2], [1, 4]]
 
 
 def test_any_key_field_can_match():
@@ -26,13 +28,19 @@ def test_any_key_field_can_match():
         {"email": "b@x.org", "phone": "222"},
         {"email": "c@x.org", "phone": "111"},
     ]
-    assert find_duplicates(records, ["email", "phone"]) == [[0, 2]]
+    assert duplicate_groups(records, keys=["email", "phone"]) == [(0, 2)]
+    with pytest.warns(DeprecationWarning, match="^find_duplicates\(\) is deprecated; use duplicate_groups\(\) instead$"):
+        assert find_duplicates(records, ["email", "phone"]) == [[0, 2]]
 
 
 def test_no_duplicates():
     records = [{"email": f"user{i}@x.org"} for i in range(50)]
-    assert find_duplicates(records, ["email"]) == []
-    assert find_duplicates([], ["email"]) == []
+    assert duplicate_groups(records, keys=["email"]) == []
+    with pytest.warns(DeprecationWarning, match="^find_duplicates\(\) is deprecated; use duplicate_groups\(\) instead$"):
+        assert find_duplicates(records, ["email"]) == []
+    assert duplicate_groups([], keys=["email"]) == []
+    with pytest.warns(DeprecationWarning, match="^find_duplicates\(\) is deprecated; use duplicate_groups\(\) instead$"):
+        assert find_duplicates([], ["email"]) == []
 
 
 def test_drop_duplicates_keeps_first():
@@ -44,6 +52,13 @@ def test_drop_duplicates_keeps_first():
     assert [r["id"] for r in drop_duplicates(records, ["email"])] == [1, 2]
 
 
+
+def test_find_duplicates_warns():
+    with pytest.warns(DeprecationWarning, match=r"^find_duplicates\(\) is deprecated; use duplicate_groups\(\) instead$"):
+        assert find_duplicates([{"email": "a"}], ["email"]) == []
+
+
 def test_keys_required():
-    with pytest.raises(ValueError):
-        find_duplicates([{"email": "a"}], [])
+    with pytest.warns(DeprecationWarning, match="^find_duplicates\(\) is deprecated; use duplicate_groups\(\) instead$"):
+        with pytest.raises(ValueError):
+            find_duplicates([{"email": "a"}], [])
`

// t08ReplacedAssert is the real t08 run r_zv7w820mf2: an existing find_duplicates assert replaced by a duplicate_groups one.
const t08ReplacedAssert = `diff --git a/tests/test_dedupe.py b/tests/test_dedupe.py
--- a/tests/test_dedupe.py
+++ b/tests/test_dedupe.py
@@ -1,6 +1,6 @@
 import pytest
 
-from gatekeeper import drop_duplicates, find_duplicates, normalise_key
+from gatekeeper import drop_duplicates, duplicate_groups, find_duplicates, normalise_key
 
 
 def test_normalise_key():
@@ -17,6 +17,7 @@ def test_single_key_groups():
         {"email": "c@x.org"},
         {"email": "b@x.org"},
     ]
+    assert duplicate_groups(records, keys=["email"]) == [(0, 2), (1, 4)]
     assert find_duplicates(records, ["email"]) == [[0, 2], [1, 4]]
 
 
@@ -26,12 +27,14 @@ def test_any_key_field_can_match():
         {"email": "b@x.org", "phone": "222"},
         {"email": "c@x.org", "phone": "111"},
     ]
-    assert find_duplicates(records, ["email", "phone"]) == [[0, 2]]
+    assert duplicate_groups(records, keys=["email", "phone"]) == [(0, 2)]
 
 
 def test_no_duplicates():
     records = [{"email": f"user{i}@x.org"} for i in range(50)]
+    assert duplicate_groups(records, keys=["email"]) == []
     assert find_duplicates(records, ["email"]) == []
+    assert duplicate_groups([], keys=["email"]) == []
     assert find_duplicates([], ["email"]) == []
 
 
@@ -47,3 +50,8 @@ def test_drop_duplicates_keeps_first():
 def test_keys_required():
     with pytest.raises(ValueError):
         find_duplicates([{"email": "a"}], [])
+
+
+def test_find_duplicates_deprecated_warning():
+    with pytest.warns(DeprecationWarning, match=r"^find_duplicates\(\) is deprecated; use duplicate_groups\(\) instead$"):
+        assert find_duplicates([{"email": "a"}, {"email": "a"}], ["email"]) == [[0, 1]]
`

// t08LeftRaises is the real t08 run r_nf4c1puwrq: an inserted pytest.warns line moves the existing find_duplicates call out of its pytest.raises block, with no line removed.
const t08LeftRaises = `diff --git a/tests/test_dedupe.py b/tests/test_dedupe.py
--- a/tests/test_dedupe.py
+++ b/tests/test_dedupe.py
@@ -1,6 +1,6 @@
 import pytest
 
-from gatekeeper import drop_duplicates, find_duplicates, normalise_key
+from gatekeeper import duplicate_groups, drop_duplicates, find_duplicates, normalise_key
 
 
 def test_normalise_key():
@@ -17,6 +17,7 @@ def test_single_key_groups():
         {"email": "c@x.org"},
         {"email": "b@x.org"},
     ]
+    assert duplicate_groups(records, keys=["email"]) == [(0, 2), (1, 4)]
     assert find_duplicates(records, ["email"]) == [[0, 2], [1, 4]]
 
 
@@ -26,11 +27,14 @@ def test_any_key_field_can_match():
         {"email": "b@x.org", "phone": "222"},
         {"email": "c@x.org", "phone": "111"},
     ]
+    assert duplicate_groups(records, keys=["email", "phone"]) == [(0, 2)]
     assert find_duplicates(records, ["email", "phone"]) == [[0, 2]]
 
 
 def test_no_duplicates():
     records = [{"email": f"user{i}@x.org"} for i in range(50)]
+    assert duplicate_groups(records, keys=["email"]) == []
+    assert duplicate_groups([], keys=["email"]) == []
     assert find_duplicates(records, ["email"]) == []
     assert find_duplicates([], ["email"]) == []
 
@@ -46,4 +50,6 @@ def test_drop_duplicates_keeps_first():
 
 def test_keys_required():
     with pytest.raises(ValueError):
+        duplicate_groups([{"email": "a"}], keys=[])
+    with pytest.warns(DeprecationWarning, match=r"^find_duplicates\(\) is deprecated; use duplicate_groups\(\) instead$"):
         find_duplicates([{"email": "a"}], [])
`

// section builds one file's section of a unified diff from its hunks.
func section(path string, hunks ...string) string {
	return "diff --git a/" + path + " b/" + path + "\n--- a/" + path + "\n+++ b/" + path + "\n" + strings.Join(hunks, "")
}

// hunkAt builds one hunk whose header counts match its body lines, each of
// which starts with ' ', '+' or '-'.
func hunkAt(oldStart, newStart int, body ...string) string {
	oldN, newN := 0, 0
	for _, l := range body {
		switch l[0] {
		case '-':
			oldN++
		case '+':
			newN++
		default:
			oldN++
			newN++
		}
	}
	return fmt.Sprintf("@@ -%d,%d +%d,%d @@\n%s\n", oldStart, oldN, newStart, newN, strings.Join(body, "\n"))
}

func TestModificationVerdict(t *testing.T) {
	const dedupe, x = "tests/test_dedupe.py", "tests/test_x.py"
	// test wraps body lines in a hunk of tests/test_x.py that opens inside
	// test_total, whose first lines are context.
	test := func(body ...string) string {
		head := []string{" def test_total():", "     records = load()"}
		return section(x, hunkAt(10, 10, append(head, body...)...))
	}
	cases := []struct {
		name   string
		path   string
		diff   string
		kept   bool
		reason string
	}{
		{"t08 superset import with new asserts beside old ones", dedupe, t08SupersetImport, true, "existing lines kept: 1 superset import"},
		{"t08 asserts wrapped in pytest.warns", dedupe, t08WrappedAsserts, true, "existing lines kept: 1 superset import, 4 moved or re-indented"},
		{"t08 pytest.raises block nested in pytest.warns", dedupe, t08NestedRaises, true, "existing lines kept: 1 superset import, 6 moved or re-indented"},
		{"t08 old assert replaced by a new-api assert", dedupe, t08ReplacedAssert, false,
			`existing line removed or changed: "assert find_duplicates(records, [\"email\", \"phone\"]) == [[0, 2]]"`},
		{"t08 existing call moved out of pytest.raises", dedupe, t08LeftRaises, false,
			`block around an existing line changed from "with pytest.raises(ValueError):" to "with pytest.warns(DeprecationWarning, match=r\"^find_duplicates`},
		{"expected value changed", x, test("-    assert total(records) == 3", "+    assert total(records) == 4"), false,
			`existing line removed or changed: "assert total(records) == 3"`},
		{"assert deleted", x, test("-    assert total(records) == 3", " ", " "), false, `existing line removed or changed: "assert total(records) == 3"`},
		{"assert weakened to >= 0", x, test("-    assert total(records) == 3", "+    assert total(records) >= 0"), false, "existing line removed or changed"},
		{"assert or True", x, test("-    assert total(records) == 3", "+    assert total(records) == 3 or True"), false, "existing line removed or changed"},
		{"assert operand extended", x, test("-    assert total(records) == 3", "+    assert total(records) == 30"), false, "existing line removed or changed"},
		{"assert re-indented inside pytest.warns", x,
			test("-    assert total(records) == 3", "+    with pytest.warns(DeprecationWarning):", "+        assert total(records) == 3"),
			true, "existing lines kept: 1 moved or re-indented"},
		{"assert re-indented inside a multi-line pytest.warns", x,
			test("-    assert total(records) == 3", "+    with pytest.warns(", `+        DeprecationWarning, match="old"`, "+    ):", "+        assert total(records) == 3"),
			true, "existing lines kept: 1 moved or re-indented"},
		{"assert re-indented inside pytest.warns as rec", x,
			test("-    assert total(records) == 3", "+    with pytest.warns(DeprecationWarning) as rec:", "+        assert total(records) == 3", "+    assert len(rec) == 1"),
			true, "existing lines kept: 1 moved or re-indented"},
		{"assert behind a one-line wrapper", x,
			test("-    assert total(records) == 3", "+    with pytest.warns(DeprecationWarning): assert total(records) == 3"),
			true, "existing lines kept: 1 assert kept with wrapper or message"},
		{"assert message added", x, test("-    assert total(records) == 3", `+    assert total(records) == 3, "total"`),
			true, "existing lines kept: 1 assert kept with wrapper or message"},
		{"assert moved under if False", x,
			test("-    assert total(records) == 3", "+    if False:", "+        assert total(records) == 3"),
			false, `existing line moved into a different block: "assert total(records) == 3"`},
		{"assert moved under pytest.raises(AssertionError)", x,
			test("-    assert total(records) == 3", "+    with pytest.raises(AssertionError):", "+        assert total(records) == 3"),
			false, "existing line moved into a different block"},
		{"assert moved under a wrapper plus suppress", x,
			test("-    assert total(records) == 3", "+    with pytest.warns(DeprecationWarning), contextlib.suppress(AssertionError):", "+        assert total(records) == 3"),
			false, "existing line moved into a different block"},
		{"assert moved into a helper nobody calls", x,
			test("-    assert total(records) == 3", "+", "+", "+def _unused(records):", "+    assert total(records) == 3"),
			false, "existing line moved into a different block"},
		{"one added line keeps only one removed line", x, section(x, hunkAt(1, 1,
			" def test_a():", "-    assert f() == 1", "+    with pytest.warns(DeprecationWarning):", "+        assert f() == 1",
			" ", " ", " def test_b():", "-    assert f() == 1")),
			false, `existing line removed or changed: "assert f() == 1"`},
		{"superset import in parentheses", x, section(x, hunkAt(1, 1, "-from m import a, b", "+from m import (a, b, c)", " ")),
			true, "existing lines kept: 1 superset import"},
		{"plain import superset", x, section(x, hunkAt(1, 1, "-import os", "+import os, sys", " ")), true, "existing lines kept: 1 superset import"},
		{"subset import", x, section(x, hunkAt(1, 1, "-from m import a, b", "+from m import a", " ")), false, `existing line removed or changed: "from m import a, b"`},
		{"import rebinds an old name", x, section(x, hunkAt(1, 1, "-from m import a", "+from m import a, b as a", " ")), false, "existing line removed or changed"},
		{"import from another module", x, section(x, hunkAt(1, 1, "-from m import a", "+from m.compat import a", " ")), false, "existing line removed or changed"},
		{"parametrize case dropped", x, section(x, hunkAt(5, 5,
			` @pytest.mark.parametrize("text", [`, `     "a",`, `-    "b",`, " ])")),
			false, `existing line removed or changed: "\"b\","`},
		{"test function removed", x, section(x, hunkAt(5, 5,
			"-def test_gone():", "-    assert f() == 1", "-", "-", " def test_kept():")),
			false, `existing line removed or changed: "def test_gone():" (and 3 more)`},
		{"skip added beside a kept import", x, section(x, hunkAt(1, 1,
			"-from m import a", "+from m import a, b", "+pytestmark = pytest.mark.skip(\"x\")", " ")),
			false, `added line disables or short-circuits tests: "pytestmark = pytest.mark.skip(\"x\")"`},
		{"inserted header captures existing lines", x, section(x, hunkAt(10, 10,
			" def test_total():", "+  if False:", "     records = load()", "     assert total(records) == 3")),
			false, `block around an existing line changed from "def test_total():" to "if False:": "records = load()"`},
		{"new wrapper beside existing lines captures nothing", x, section(x, hunkAt(10, 10,
			" def test_total():", "+    with pytest.warns(DeprecationWarning):", "+        legacy_total([])", "     records = load()")),
			true, "only new lines added"},
		{"list items under a decorator stay put when a test is added above", x, section(x, hunkAt(30, 30,
			"     assert isinstance(cfg[\"limiter\"][\"window\"], float)", " ", " ",
			`+@pytest.mark.parametrize("env,value", [`, `+    ({"A": ""}, "A"),`, "+])", "+def test_env(env, value):", "+    assert load(env) == value", "+", "+",
			` @pytest.mark.parametrize("text", [`, `     "[limiter]\nlimt = 5\n",`, `     "colour = 'blue'\n",`, " ])")),
			true, "only new lines added"},
		{"string content ending in a colon is not a block", x, section(x, hunkAt(10, 10,
			" def test_total():", `+    text = """`, "+header:", `+"""`, "     records = load()")),
			true, "only new lines added"},
		{"parameter inserted into a multi-line test signature", x, section(x, hunkAt(10, 10,
			" def test_total(", "+    monkeypatch,", " ):", "     records = load()")),
			false, `block around an existing line changed from "def test_total( ):" to "def test_total( monkeypatch, ):"`},
		{"multi-line wrapper header around a moved assert", x, section(x, hunkAt(10, 10,
			" def test_total(", "     tmp_path,", " ):", "-    assert total() == 3", "+    with pytest.warns(", "+        DeprecationWarning,", "+    ):", "+        assert total() == 3")),
			true, "existing lines kept: 1 moved or re-indented"},
		{"list items re-indented by a reformat still lose the changed brackets", x, section(x, hunkAt(5, 5,
			`-@pytest.mark.parametrize("text", [`, `-    "a",`, "-])", "+@pytest.mark.parametrize(", `+    "text",`, "+    [", `+        "a",`, "+    ],", "+)", " def test_text(text):")),
			false, `existing line removed or changed: "@pytest.mark.parametrize(\"text\", [" (and 2 more)`},
		{"parametrize case moved to another test's list", x, section(x, hunkAt(5, 5,
			` @pytest.mark.parametrize("text", [`, `     "a",`, `-    "b",`, " ])", " def test_text(text):", "     check(text)", " ",
			" ", ` @pytest.mark.parametrize("other", [`, `+    "b",`, `     "c",`, " ])")),
			false, `existing line moved into a different block: "\"b\","`},
		{"inserted lines close a list early", x, section(x, hunkAt(5, 5,
			` @pytest.mark.parametrize("text", [`, `     "a",`, "+])", `+@pytest.mark.parametrize("other", [`, `     "b",`, " ])")),
			false, `statement around an existing line changed from "@pytest.mark.parametrize(\"text\", [" to "@pytest.mark.parametrize(\"other\", [": "\"b\","`},
		{"multi-line assert re-indented inside pytest.warns", x,
			test("-    assert total(", "-        records) == 3", "+    with pytest.warns(DeprecationWarning):", "+        assert total(", "+        records) == 3"),
			true, "existing lines kept: 2 moved or re-indented"},
		{"triple-quoted expected text re-indented", x,
			test(`-    assert render() == """a`, `-  b"""`, "+    with pytest.warns(DeprecationWarning):", `+        assert render() == """a`, `+      b"""`),
			false, `existing line removed or changed: "b\"\"\""`},
		{"triple-quoted expected text moved whole", x,
			test(`-    assert render() == """a`, `-  b"""`, "+    with pytest.warns(DeprecationWarning):", `+        assert render() == """a`, `+  b"""`),
			true, "existing lines kept: 2 moved or re-indented"},
		{"no section for the file", "tests/other.py", t08SupersetImport, false, "no diff section"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			files, err := parseUnifiedDiff([]byte(c.diff))
			if err != nil {
				t.Fatal(err)
			}
			kept, why := modificationVerdict(files, c.path)
			if kept != c.kept || !strings.Contains(why, c.reason) {
				t.Fatalf("kept=%v reason=%q, want kept=%v reason containing %q", kept, why, c.kept, c.reason)
			}
		})
	}
}

func TestTestEditGatesOnT08(t *testing.T) {
	cases := []struct {
		name   string
		diff   string
		fired  bool
		detail string
	}{
		{"superset import", t08SupersetImport, false, "every existing line kept: tests/test_dedupe.py (existing lines kept: 1 superset import)"},
		{"wrapped asserts", t08WrappedAsserts, false, "every existing line kept"},
		{"nested raises", t08NestedRaises, false, "every existing line kept"},
		{"replaced assert", t08ReplacedAssert, true, "tests/test_dedupe.py (modified: existing line removed or changed: "},
		{"call left pytest.raises", t08LeftRaises, true, "tests/test_dedupe.py (modified: block around an existing line changed from "},
	}
	rc := &schemas.RunContext{
		Task:  &schemas.Task{ID: "t08", Protected: []string{"tests/**"}},
		Suite: &schemas.Suite{ID: "coding-core", Verifier: schemas.SuiteVerifier{Restore: []string{"tests"}}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			ev := &schemas.Evidence{Diff: &schemas.Diff{
				Path:  writeDiff(t, c.diff),
				Files: []schemas.DiffFile{{Path: "tests/test_dedupe.py", Status: "modified"}},
			}}
			for _, g := range []schemas.Gate{newProtectedPathsGate(), newRegradeGate()} {
				res, err := g.Check(context.Background(), rc, ev)
				if err != nil {
					t.Fatal(err)
				}
				if res.Fired != c.fired || !strings.Contains(res.Detail, c.detail) || res.Version != testEditVersion {
					t.Fatalf("%s: fired=%v version=%s detail=%q, want fired=%v detail containing %q",
						g.GetName(), res.Fired, res.Version, res.Detail, c.fired, c.detail)
				}
			}
		})
	}
}
