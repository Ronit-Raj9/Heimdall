package annotate

import "testing"

// N72: a workspace diff that only adds files becomes each file's name and
// content; any other diff is labelled as it is.
func TestAnswerFromDiffNewFiles(t *testing.T) {
	two := "diff --git a/result.json b/result.json\nnew file mode 100644\nindex 0000000..1111111\n--- /dev/null\n+++ b/result.json\n@@ -0,0 +1,2 @@\n+[1,\n+ 2]\n" +
		"diff --git a/notes/a.txt b/notes/a.txt\n--- /dev/null\n+++ b/notes/a.txt\n@@ -0,0 +1 @@\n++plus kept\n\\ No newline at end of file\n"
	if got, want := AnswerFromDiff([]byte(two)), "result.json:\n[1,\n 2]\n\nnotes/a.txt:\n+plus kept\n"; got != want {
		t.Fatalf("new files:\n%q\nwant\n%q", got, want)
	}
	changed := "diff --git a/x.py b/x.py\n--- a/x.py\n+++ b/x.py\n@@ -1 +1 @@\n-a\n+b\n"
	if got := AnswerFromDiff([]byte(two + changed)); got != two+changed {
		t.Fatalf("a changed file must keep the diff, got %q", got)
	}
	if got := AnswerFromDiff([]byte("  \n")); got != "" {
		t.Fatalf("blank diff: %q", got)
	}
	if got := AnswerFromDiff([]byte("not a diff\n")); got != "not a diff\n" {
		t.Fatalf("text without blocks: %q", got)
	}
}
