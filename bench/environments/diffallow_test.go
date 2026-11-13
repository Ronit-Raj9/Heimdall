package environments

import (
	"reflect"
	"strings"
	"testing"

	"github.com/maximhq/bifrost/bench/schemas"
)

const (
	secModified = "diff --git a/calc/__init__.py b/calc/__init__.py\n" +
		"index c83cf5c..39a55dd 100644\n" +
		"--- a/calc/__init__.py\n" +
		"+++ b/calc/__init__.py\n" +
		"@@ -1 +1 @@\n" +
		"-    return a - b\n" +
		"+    return a + b\n"
	secIgnoredOutput = "diff --git a/out/result.txt b/out/result.txt\n" +
		"new file mode 100644\n" +
		"index 0000000..19f86f4\n" +
		"--- /dev/null\n" +
		"+++ b/out/result.txt\n" +
		"@@ -0,0 +1 @@\n" +
		"+done\n"
	secSymlink = "diff --git a/link b/link\n" +
		"new file mode 120000\n" +
		"index 0000000..e69de29\n" +
		"--- /dev/null\n" +
		"+++ b/link\n" +
		"@@ -0,0 +1 @@\n" +
		"+/etc/passwd\n" +
		"\\ No newline at end of file\n"
	secSubmodule = "diff --git a/vendor/lib b/vendor/lib\n" +
		"new file mode 160000\n" +
		"index 0000000..1234567\n" +
		"--- /dev/null\n" +
		"+++ b/vendor/lib\n" +
		"@@ -0,0 +1 @@\n" +
		"+Subproject commit 1234567890123456789012345678901234567890\n"
	secDotDot = "diff --git a/../escape.py b/../escape.py\n" +
		"new file mode 100644\n" +
		"--- /dev/null\n" +
		"+++ b/../escape.py\n" +
		"@@ -0,0 +1 @@\n" +
		"+x\n"
	secAbsolute = "diff --git a//etc/cron.d/x b//etc/cron.d/x\n" +
		"new file mode 100644\n" +
		"--- /dev/null\n" +
		"+++ b//etc/cron.d/x\n" +
		"@@ -0,0 +1 @@\n" +
		"+x\n"
	secPycache = "diff --git a/calc/__pycache__/__init__.cpython-312.pyc b/calc/__pycache__/__init__.cpython-312.pyc\n" +
		"new file mode 100644\n" +
		"index 0000000..0d5bd2e\n" +
		"GIT binary patch\n" +
		"literal 5\n" +
		"McmZQzWMXCj000310RR91\n" +
		"\n" +
		"literal 0\n" +
		"HcmV?d00001\n" +
		"\n"
	secPytestCache = "diff --git a/.pytest_cache/v/cache/lastfailed b/.pytest_cache/v/cache/lastfailed\n" +
		"new file mode 100644\n" +
		"--- /dev/null\n" +
		"+++ b/.pytest_cache/v/cache/lastfailed\n" +
		"@@ -0,0 +1 @@\n" +
		"+{}\n"
	secStrayPyc = "diff --git a/old.pyc b/old.pyc\n" +
		"new file mode 100644\n" +
		"Binary files /dev/null and b/old.pyc differ\n"
	secDeleted = "diff --git a/README.md b/README.md\n" +
		"deleted file mode 100644\n" +
		"index 3b18e51..0000000\n" +
		"--- a/README.md\n" +
		"+++ /dev/null\n" +
		"@@ -1 +0,0 @@\n" +
		"-hello\n"
	secModeOnly = "diff --git a/run.sh b/run.sh\n" +
		"old mode 100644\n" +
		"new mode 100755\n"
	secSpaces = "diff --git a/my notes.txt b/my notes.txt\n" +
		"new file mode 100644\n" +
		"--- /dev/null\n" +
		"+++ b/my notes.txt\t\n" +
		"@@ -0,0 +1 @@\n" +
		"+x\n"
	secQuoted = "diff --git \"a/caf\\303\\251 \\\"x\\\".txt\" \"b/caf\\303\\251 \\\"x\\\".txt\"\n" +
		"new file mode 100644\n" +
		"--- /dev/null\n" +
		"+++ \"b/caf\\303\\251 \\\"x\\\".txt\"\n" +
		"@@ -0,0 +1 @@\n" +
		"+x\n"
	secDevice = "diff --git a/dev/null0 b/dev/null0\n" +
		"new file mode 020644\n"
	secTypeChange = "diff --git a/data b/data\n" +
		"old mode 100644\n" +
		"new mode 120000\n"
)

func TestFilterDiff(t *testing.T) {
	tests := []struct {
		name     string
		raw      string
		kept     string
		files    []schemas.DiffFile
		rejected []schemas.DiffReject
	}{
		{
			name: "empty diff",
			raw:  "",
			kept: "",
		},
		{
			name:  "modified file kept",
			raw:   secModified,
			kept:  secModified,
			files: []schemas.DiffFile{{Path: "calc/__init__.py", Status: StatusModified}},
		},
		{
			name:  "gitignored output file forced into the diff is kept",
			raw:   secIgnoredOutput,
			kept:  secIgnoredOutput,
			files: []schemas.DiffFile{{Path: "out/result.txt", Status: StatusAdded}},
		},
		{
			name:     "symlink rejected",
			raw:      secModified + secSymlink,
			kept:     secModified,
			files:    []schemas.DiffFile{{Path: "calc/__init__.py", Status: StatusModified}},
			rejected: []schemas.DiffReject{{Path: "link", Reason: RejectSymlink}},
		},
		{
			name:     "change to a symlink rejected",
			raw:      secTypeChange,
			kept:     "",
			rejected: []schemas.DiffReject{{Path: "data", Reason: RejectSymlink}},
		},
		{
			name:     "submodule rejected",
			raw:      secSubmodule,
			kept:     "",
			rejected: []schemas.DiffReject{{Path: "vendor/lib", Reason: RejectSubmodule}},
		},
		{
			name:     "dot dot path rejected",
			raw:      secDotDot + secModified,
			kept:     secModified,
			files:    []schemas.DiffFile{{Path: "calc/__init__.py", Status: StatusModified}},
			rejected: []schemas.DiffReject{{Path: "../escape.py", Reason: RejectOutside}},
		},
		{
			name:     "absolute path rejected",
			raw:      secAbsolute,
			kept:     "",
			rejected: []schemas.DiffReject{{Path: "/etc/cron.d/x", Reason: RejectOutside}},
		},
		{
			name:     "unknown file mode rejected as device",
			raw:      secDevice,
			kept:     "",
			rejected: []schemas.DiffReject{{Path: "dev/null0", Reason: RejectDevice}},
		},
		{
			name: "cache paths dropped with reason cache",
			raw:  secPycache + secModified + secPytestCache + secStrayPyc,
			kept: secModified,
			files: []schemas.DiffFile{
				{Path: "calc/__init__.py", Status: StatusModified},
			},
			rejected: []schemas.DiffReject{
				{Path: "calc/__pycache__/__init__.cpython-312.pyc", Reason: RejectCache},
				{Path: ".pytest_cache/v/cache/lastfailed", Reason: RejectCache},
				{Path: "old.pyc", Reason: RejectCache},
			},
		},
		{
			name: "deleted, mode-only, spaced and quoted paths kept",
			raw:  secDeleted + secModeOnly + secSpaces + secQuoted,
			kept: secDeleted + secModeOnly + secSpaces + secQuoted,
			files: []schemas.DiffFile{
				{Path: "README.md", Status: StatusDeleted},
				{Path: "run.sh", Status: StatusModified},
				{Path: "my notes.txt", Status: StatusAdded},
				{Path: "café \"x\".txt", Status: StatusAdded},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := filterDiff([]byte(tt.raw))
			if err != nil {
				t.Fatalf("filterDiff: %v", err)
			}
			if string(got.Kept) != tt.kept {
				t.Errorf("kept:\n%s\nwant:\n%s", got.Kept, tt.kept)
			}
			if !reflect.DeepEqual(got.Files, tt.files) {
				t.Errorf("files = %+v, want %+v", got.Files, tt.files)
			}
			if !reflect.DeepEqual(got.Rejected, tt.rejected) {
				t.Errorf("rejected = %+v, want %+v", got.Rejected, tt.rejected)
			}
		})
	}
}

func TestFilterDiffErrors(t *testing.T) {
	tests := []struct {
		name string
		raw  string
		want string
	}{
		{"text before first header", "hello\n" + secModified, "before the first diff header"},
		{"unparseable header", "diff --git a/x b/y\n", "cannot find the path"},
		{"unterminated quote", "diff --git \"a/x\n", "unterminated quote"},
		{"bad marker prefix", "diff --git a/x b/x\n--- c/x\n", "missing a/ prefix"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := filterDiff([]byte(tt.raw))
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("err = %v, want it to contain %q", err, tt.want)
			}
		})
	}
}

func TestUnquoteC(t *testing.T) {
	tests := []struct {
		in, want, rest string
		wantErr        bool
	}{
		{in: `"a/b" tail`, want: "a/b", rest: " tail"},
		{in: `"tab\there\\"`, want: "tab\there\\"},
		{in: `"\303\251"`, want: "é"},
		{in: `"bad\9"`, wantErr: true},
		{in: `noquote`, wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.in, func(t *testing.T) {
			got, rest, err := unquoteC(tt.in)
			if (err != nil) != tt.wantErr {
				t.Fatalf("err = %v, wantErr %v", err, tt.wantErr)
			}
			if !tt.wantErr && (got != tt.want || rest != tt.rest) {
				t.Fatalf("got %q rest %q, want %q rest %q", got, rest, tt.want, tt.rest)
			}
		})
	}
}
