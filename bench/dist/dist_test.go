package dist

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/maximhq/bifrost/bench/handlers"
)

const sdk = "../clients/python"

func TestReadPyProjectSDK(t *testing.T) {
	p, err := ReadPyProject(filepath.Join(sdk, "pyproject.toml"))
	if err != nil {
		t.Fatal(err)
	}
	if p.Name != "heimdall-bench" || p.DistName() != "heimdall_bench" || p.Version == "" || p.RequiresPython != ">=3.9" ||
		p.License != "Apache-2.0" || len(p.Classifiers) != 2 || len(p.Packages) != 1 || p.Packages[0] != "heimdall" {
		t.Fatalf("pyproject: %+v", p)
	}
}

func TestReadPyProjectRefusesDependencies(t *testing.T) {
	dir := t.TempDir()
	src := "[project]\nname = \"x\"\nversion = \"1.0\"\ndependencies = [\n  \"requests\",\n]\n[tool.setuptools]\npackages = [\"x\"]\n"
	if err := os.WriteFile(filepath.Join(dir, "pyproject.toml"), []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadPyProject(filepath.Join(dir, "pyproject.toml")); err == nil || !strings.Contains(err.Error(), "requests") {
		t.Fatalf("dependencies accepted: %v", err)
	}
}

// TestWheelIsValidAndReproducible: every RECORD line matches its file, the
// metadata names the package, and two builds are byte for byte equal.
func TestWheelIsValidAndReproducible(t *testing.T) {
	a, b := t.TempDir(), t.TempDir()
	pa, err := BuildWheel(sdk, a)
	if err != nil {
		t.Fatal(err)
	}
	pb, err := BuildWheel(sdk, b)
	if err != nil {
		t.Fatal(err)
	}
	ra, _ := os.ReadFile(pa)
	rb, _ := os.ReadFile(pb)
	if !bytes.Equal(ra, rb) {
		t.Fatal("two wheel builds differ")
	}
	if !strings.HasSuffix(pa, "-py3-none-any.whl") || !strings.HasPrefix(filepath.Base(pa), "heimdall_bench-") {
		t.Fatalf("wheel name %s", pa)
	}
	zr, err := zip.NewReader(bytes.NewReader(ra), int64(len(ra)))
	if err != nil {
		t.Fatal(err)
	}
	files := map[string][]byte{}
	for _, f := range zr.File {
		rc, _ := f.Open()
		data, _ := io.ReadAll(rc)
		rc.Close()
		files[f.Name] = data
	}
	var record, meta []byte
	for name, data := range files {
		switch {
		case strings.HasSuffix(name, ".dist-info/RECORD"):
			record = data
		case strings.HasSuffix(name, ".dist-info/METADATA"):
			meta = data
		}
	}
	if !bytes.Contains(meta, []byte("Name: heimdall-bench\n")) || !bytes.Contains(meta, []byte("Requires-Python: >=3.9\n")) {
		t.Fatalf("METADATA:\n%s", meta)
	}
	if _, ok := files["heimdall/__init__.py"]; !ok {
		t.Fatal("package missing from the wheel")
	}
	lines := strings.Split(strings.TrimSpace(string(record)), "\n")
	if len(lines) != len(files) {
		t.Fatalf("RECORD lists %d of %d files", len(lines), len(files))
	}
	for _, l := range lines {
		parts := strings.Split(l, ",")
		if strings.HasSuffix(parts[0], "/RECORD") {
			continue
		}
		data := files[parts[0]]
		sum := sha256.Sum256(data)
		if want := fmt.Sprintf("%s,sha256=%s,%d", parts[0], base64.RawURLEncoding.EncodeToString(sum[:]), len(data)); l != want {
			t.Errorf("RECORD line %q, want %q", l, want)
		}
	}
}

func tarNames(t *testing.T, path string) map[string]int64 {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	gz, err := gzip.NewReader(bytes.NewReader(raw))
	if err != nil {
		t.Fatal(err)
	}
	tr := tar.NewReader(gz)
	out := map[string]int64{}
	for {
		h, err := tr.Next()
		if err == io.EOF {
			return out
		}
		if err != nil {
			t.Fatal(err)
		}
		out[h.Name] = h.Mode
	}
}

func TestSdist(t *testing.T) {
	p, err := BuildSdist(sdk, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	base := strings.TrimSuffix(filepath.Base(p), ".tar.gz")
	names := tarNames(t, p)
	for _, want := range []string{"/pyproject.toml", "/PKG-INFO", "/README.md", "/heimdall/__init__.py", "/heimdall/py.typed"} {
		if _, ok := names[base+want]; !ok {
			t.Errorf("sdist lacks %s", base+want)
		}
	}
}

func TestSourceTarball(t *testing.T) {
	root := t.TempDir()
	for name, mode := range map[string]os.FileMode{"go.mod": 0o644, "deploy/run.sh": 0o755, "ui/node_modules/x.js": 0o644, ".git/HEAD": 0o644} {
		p := filepath.Join(root, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(name), mode); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := BuildSource(root, t.TempDir(), "not a commit"); err == nil {
		t.Fatal("bad commit accepted")
	}
	out := filepath.Join(root, "out") // inside root: never packed into itself
	p, err := BuildSource(root, out, "0123abcd")
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Base(p) != "bench-0123abcd.tar.gz" {
		t.Fatalf("name %s", p)
	}
	names := tarNames(t, p)
	if names["bench/go.mod"] != 0o644 || names["bench/deploy/run.sh"] != 0o755 {
		t.Fatalf("entries %v", names)
	}
	for n := range names {
		if strings.Contains(n, "node_modules") || strings.Contains(n, ".git/") || strings.Contains(n, "out/") {
			t.Errorf("packed %s", n)
		}
	}
}

func TestClassifyAndManifest(t *testing.T) {
	for name, want := range map[string]string{
		"bifrost-bench-linux-amd64":             handlers.DownloadCLI,
		"bifrost-bench-darwin-arm64":            handlers.DownloadCLI,
		"heimdall_bench-0.2.0-py3-none-any.whl": handlers.DownloadPythonWheel,
		"heimdall_bench-0.2.0.tar.gz":           handlers.DownloadPythonSdist,
		"heimdall-bench-client-0.2.0.tgz":       handlers.DownloadTypeScript,
		"bench-0123abcd.tar.gz":                 handlers.DownloadSource,
		"notes.txt":                             "",
	} {
		if got, _, _ := Classify(name); got != want {
			t.Errorf("Classify(%s) = %q, want %q", name, got, want)
		}
	}
	if _, goos, goarch := Classify("bifrost-bench-darwin-arm64"); goos != "darwin" || goarch != "arm64" {
		t.Errorf("os/arch %s %s", goos, goarch)
	}
	dir := t.TempDir()
	for _, n := range []string{"bifrost-bench-linux-amd64", "heimdall-bench-client-0.2.0.tgz"} {
		if err := os.WriteFile(filepath.Join(dir, n), []byte(n), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	m, err := WriteManifest(dir, "0123abcd", "0.1.0", time.Unix(0, 0))
	if err != nil || len(m.Files) != 2 || !m.Available {
		t.Fatalf("manifest %+v %v", m, err)
	}
	raw, _ := os.ReadFile(filepath.Join(dir, handlers.DownloadsManifestFile))
	var back handlers.DownloadsManifest
	if err := json.Unmarshal(raw, &back); err != nil || back.Commit != "0123abcd" || back.Files[0].SHA256 == "" {
		t.Fatalf("manifest.json %s %v", raw, err)
	}
	sums, _ := os.ReadFile(filepath.Join(dir, handlers.DownloadsChecksumsFile))
	sum := sha256.Sum256([]byte("bifrost-bench-linux-amd64"))
	if !strings.Contains(string(sums), fmt.Sprintf("%x  bifrost-bench-linux-amd64\n", sum)) {
		t.Fatalf("SHA256SUMS:\n%s", sums)
	}
	// Rewriting over its own output works; an unknown file is refused.
	if _, err := WriteManifest(dir, "0123abcd", "0.1.0", time.Time{}); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "notes.txt"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := WriteManifest(dir, "0123abcd", "0.1.0", time.Time{}); err == nil {
		t.Fatal("unknown file listed")
	}
}
