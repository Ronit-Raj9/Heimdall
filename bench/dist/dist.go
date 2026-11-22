// Package dist builds the files a Heimdall server offers at /downloads
// (lane Y08, audit G4-09): the Python SDK wheel and sdist, a source tarball
// of the bench module, SHA256SUMS and manifest.json. The command line tool
// binaries and the TypeScript tarball are built by the Go and npm toolchains
// in the server image (deploy/hosted/Dockerfile.server); this package only
// packages, checksums and lists them. Nothing is published anywhere: the
// server serves the folder itself.
//
// Builds are reproducible: entries are sorted and carry a fixed time.
package dist

import (
	"archive/tar"
	"archive/zip"
	"bufio"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/maximhq/bifrost/bench/handlers"
)

// Epoch is the time every archive entry carries (zip cannot go before 1980).
var Epoch = time.Date(1980, 1, 1, 0, 0, 0, 0, time.UTC)

// PyProject is the part of a pyproject.toml [project] table a pure Python
// wheel needs.
type PyProject struct {
	Name           string
	Version        string
	Summary        string
	RequiresPython string
	License        string
	Classifiers    []string
	Packages       []string // [tool.setuptools] packages
	Readme         string
}

var (
	tomlKeyRe  = regexp.MustCompile(`^([A-Za-z0-9_.-]+)\s*=\s*(.*)$`)
	tomlStrRe  = regexp.MustCompile(`^"((?:[^"\\]|\\.)*)"`)
	pyNameRe   = regexp.MustCompile(`^[A-Za-z0-9]([A-Za-z0-9._-]*[A-Za-z0-9])?$`)
	pyVerRe    = regexp.MustCompile(`^[0-9]+(\.[0-9]+)*([a-z0-9.+-]*)$`)
	normNameRe = regexp.MustCompile(`[-_.]+`)
)

// ReadPyProject reads the fields PyProject holds from a pyproject.toml. It
// understands the plain subset the SDK file uses (strings, string arrays,
// an inline license table) and refuses runtime dependencies, which a wheel
// built here would not resolve.
func ReadPyProject(path string) (PyProject, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return PyProject{}, err
	}
	var p PyProject
	table := ""
	var pendingKey string
	var pending []string
	sc := bufio.NewScanner(bytes.NewReader(raw))
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if pendingKey != "" {
			if line == "]" || strings.HasPrefix(line, "]") {
				if err := p.set(table, pendingKey, pending); err != nil {
					return PyProject{}, err
				}
				pendingKey, pending = "", nil
				continue
			}
			for _, part := range strings.Split(line, ",") {
				if s, ok := tomlString(strings.TrimSpace(part)); ok {
					pending = append(pending, s)
				}
			}
			continue
		}
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if strings.HasPrefix(line, "[") {
			table = strings.Trim(line, "[] ")
			continue
		}
		m := tomlKeyRe.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		k, v := m[1], strings.TrimSpace(m[2])
		switch {
		case strings.HasPrefix(v, "[") && !strings.HasSuffix(v, "]"):
			pendingKey, pending = k, nil
			for _, part := range strings.Split(strings.TrimPrefix(v, "["), ",") {
				if s, ok := tomlString(strings.TrimSpace(part)); ok {
					pending = append(pending, s)
				}
			}
		case strings.HasPrefix(v, "["):
			var list []string
			for _, part := range strings.Split(strings.Trim(v, "[]"), ",") {
				if s, ok := tomlString(strings.TrimSpace(part)); ok {
					list = append(list, s)
				}
			}
			if err := p.set(table, k, list); err != nil {
				return PyProject{}, err
			}
		case strings.HasPrefix(v, "{"):
			if table == "project" && k == "license" {
				inner := strings.Trim(v, "{} ")
				if mm := tomlKeyRe.FindStringSubmatch(inner); mm != nil && mm[1] == "text" {
					p.License, _ = tomlString(strings.TrimSpace(mm[2]))
				}
			}
		default:
			if s, ok := tomlString(v); ok {
				if err := p.set(table, k, s); err != nil {
					return PyProject{}, err
				}
			}
		}
	}
	if pendingKey != "" {
		return PyProject{}, fmt.Errorf("%s: unterminated array %s", path, pendingKey)
	}
	if !pyNameRe.MatchString(p.Name) || !pyVerRe.MatchString(p.Version) {
		return PyProject{}, fmt.Errorf("%s: want [project] name and version, got %q %q", path, p.Name, p.Version)
	}
	if len(p.Packages) == 0 {
		return PyProject{}, fmt.Errorf("%s: want [tool.setuptools] packages", path)
	}
	return p, nil
}

func (p *PyProject) set(table, key string, v any) error {
	switch table {
	case "project":
		switch key {
		case "name":
			p.Name, _ = v.(string)
		case "version":
			p.Version, _ = v.(string)
		case "description":
			p.Summary, _ = v.(string)
		case "requires-python":
			p.RequiresPython, _ = v.(string)
		case "readme":
			p.Readme, _ = v.(string)
		case "classifiers":
			p.Classifiers, _ = v.([]string)
		case "dependencies":
			if l, _ := v.([]string); len(l) > 0 {
				return fmt.Errorf("runtime dependencies %v: the server-built wheel supports standard-library-only packages", l)
			}
		}
	case "tool.setuptools":
		if key == "packages" {
			p.Packages, _ = v.([]string)
		}
	}
	return nil
}

func tomlString(s string) (string, bool) {
	m := tomlStrRe.FindStringSubmatch(s)
	if m == nil {
		return "", false
	}
	out, err := jsonUnquote(`"` + m[1] + `"`)
	if err != nil {
		return m[1], true
	}
	return out, true
}

func jsonUnquote(s string) (string, error) {
	var out string
	err := json.Unmarshal([]byte(s), &out)
	return out, err
}

// DistName is the normalized distribution name used in file names
// (heimdall-bench -> heimdall_bench).
func (p PyProject) DistName() string {
	return strings.ToLower(normNameRe.ReplaceAllString(p.Name, "_"))
}

// Metadata is the core metadata (METADATA, PKG-INFO) of the package.
func (p PyProject) Metadata(readme []byte) []byte {
	var b bytes.Buffer
	fmt.Fprintf(&b, "Metadata-Version: 2.1\nName: %s\nVersion: %s\n", p.Name, p.Version)
	if p.Summary != "" {
		fmt.Fprintf(&b, "Summary: %s\n", p.Summary)
	}
	if p.License != "" {
		fmt.Fprintf(&b, "License: %s\n", p.License)
	}
	for _, c := range p.Classifiers {
		fmt.Fprintf(&b, "Classifier: %s\n", c)
	}
	if p.RequiresPython != "" {
		fmt.Fprintf(&b, "Requires-Python: %s\n", p.RequiresPython)
	}
	if len(readme) > 0 {
		b.WriteString("Description-Content-Type: text/markdown\n\n")
		b.Write(readme)
		if !bytes.HasSuffix(readme, []byte("\n")) {
			b.WriteByte('\n')
		}
	}
	return b.Bytes()
}

// packageFiles lists the files of the packages under src (slash paths
// relative to src), skipping caches and compiled files, sorted.
func (p PyProject) packageFiles(src string) ([]string, error) {
	var out []string
	for _, pkg := range p.Packages {
		if strings.Contains(pkg, "..") || strings.ContainsAny(pkg, `/\`) {
			return nil, fmt.Errorf("package %q", pkg)
		}
		root := filepath.Join(src, pkg)
		err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() {
				if d.Name() == "__pycache__" {
					return filepath.SkipDir
				}
				return nil
			}
			if !d.Type().IsRegular() || strings.HasSuffix(d.Name(), ".pyc") {
				return nil
			}
			rel, err := filepath.Rel(src, path)
			if err != nil {
				return err
			}
			out = append(out, filepath.ToSlash(rel))
			return nil
		})
		if err != nil {
			return nil, err
		}
	}
	sort.Strings(out)
	if len(out) == 0 {
		return nil, errors.New("no package files")
	}
	return out, nil
}

// BuildWheel writes <dist>-<version>-py3-none-any.whl for the pure Python
// project in src into out and returns its path.
func BuildWheel(src, out string) (string, error) {
	p, err := ReadPyProject(filepath.Join(src, "pyproject.toml"))
	if err != nil {
		return "", err
	}
	files, err := p.packageFiles(src)
	if err != nil {
		return "", err
	}
	readme := readReadme(src, p)
	info := p.DistName() + "-" + p.Version + ".dist-info"
	type entry struct {
		name string
		data []byte
	}
	var entries []entry
	for _, f := range files {
		data, err := os.ReadFile(filepath.Join(src, filepath.FromSlash(f)))
		if err != nil {
			return "", err
		}
		entries = append(entries, entry{f, data})
	}
	top := append([]string(nil), p.Packages...)
	sort.Strings(top)
	entries = append(entries,
		entry{info + "/METADATA", p.Metadata(readme)},
		entry{info + "/WHEEL", []byte("Wheel-Version: 1.0\nGenerator: heimdall-dist\nRoot-Is-Purelib: true\nTag: py3-none-any\n")},
		entry{info + "/top_level.txt", []byte(strings.Join(top, "\n") + "\n")},
	)
	var record bytes.Buffer
	for _, e := range entries {
		sum := sha256.Sum256(e.data)
		fmt.Fprintf(&record, "%s,sha256=%s,%d\n", e.name, base64.RawURLEncoding.EncodeToString(sum[:]), len(e.data))
	}
	fmt.Fprintf(&record, "%s/RECORD,,\n", info)
	entries = append(entries, entry{info + "/RECORD", record.Bytes()})

	name := fmt.Sprintf("%s-%s-py3-none-any.whl", p.DistName(), p.Version)
	path := filepath.Join(out, name)
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for _, e := range entries {
		h := &zip.FileHeader{Name: e.name, Method: zip.Deflate, Modified: Epoch}
		h.SetMode(0o644)
		w, err := zw.CreateHeader(h)
		if err != nil {
			return "", err
		}
		if _, err := w.Write(e.data); err != nil {
			return "", err
		}
	}
	if err := zw.Close(); err != nil {
		return "", err
	}
	return path, writeFile(path, buf.Bytes())
}

// BuildSdist writes <dist>-<version>.tar.gz: pyproject.toml, the readme,
// PKG-INFO and the package files under <dist>-<version>/.
func BuildSdist(src, out string) (string, error) {
	p, err := ReadPyProject(filepath.Join(src, "pyproject.toml"))
	if err != nil {
		return "", err
	}
	files, err := p.packageFiles(src)
	if err != nil {
		return "", err
	}
	readme := readReadme(src, p)
	base := p.DistName() + "-" + p.Version
	tf := &tarFiles{}
	for _, f := range append([]string{"pyproject.toml", readmeName(p)}, files...) {
		data, err := os.ReadFile(filepath.Join(src, filepath.FromSlash(f)))
		if err != nil {
			if f == readmeName(p) && errors.Is(err, os.ErrNotExist) {
				continue
			}
			return "", err
		}
		tf.add(base+"/"+f, data, 0o644)
	}
	tf.add(base+"/PKG-INFO", p.Metadata(readme), 0o644)
	data, err := tf.gzip()
	if err != nil {
		return "", err
	}
	path := filepath.Join(out, base+".tar.gz")
	return path, writeFile(path, data)
}

func readmeName(p PyProject) string {
	if p.Readme != "" && !strings.Contains(p.Readme, "..") {
		return p.Readme
	}
	return "README.md"
}

func readReadme(src string, p PyProject) []byte {
	data, _ := os.ReadFile(filepath.Join(src, readmeName(p)))
	return data
}

// tarFiles collects files for a deterministic tar.gz.
type tarFiles struct {
	names []string
	data  map[string][]byte
	modes map[string]int64
}

func (t *tarFiles) add(name string, data []byte, mode int64) {
	if t.data == nil {
		t.data, t.modes = map[string][]byte{}, map[string]int64{}
	}
	if _, dup := t.data[name]; !dup {
		t.names = append(t.names, name)
	}
	t.data[name], t.modes[name] = data, mode
}

func (t *tarFiles) gzip() ([]byte, error) {
	sort.Strings(t.names)
	var buf bytes.Buffer
	gz, err := gzip.NewWriterLevel(&buf, gzip.BestCompression)
	if err != nil {
		return nil, err
	}
	gz.ModTime = Epoch
	tw := tar.NewWriter(gz)
	for _, n := range t.names {
		d := t.data[n]
		if err := tw.WriteHeader(&tar.Header{Name: n, Mode: t.modes[n], Size: int64(len(d)), ModTime: Epoch, Typeflag: tar.TypeReg, Format: tar.FormatPAX}); err != nil {
			return nil, err
		}
		if _, err := tw.Write(d); err != nil {
			return nil, err
		}
	}
	if err := tw.Close(); err != nil {
		return nil, err
	}
	if err := gz.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// SourceSkip are folders a source tarball leaves out wherever they are.
var SourceSkip = map[string]bool{".git": true, "node_modules": true, ".heimdall": true, "__pycache__": true}

// BuildSource writes bench-<commit>.tar.gz: every regular file under root
// (the bench module as the image build sees it, built UI included) below a
// top-level bench/ folder, without SourceSkip folders. Executable bits are
// kept; symbolic links are left out.
func BuildSource(root, out, commit string) (string, error) {
	if !commitRe.MatchString(commit) {
		return "", fmt.Errorf("commit %q: want a git commit (hex)", commit)
	}
	absOut, _ := filepath.Abs(out)
	tf := &tarFiles{}
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if abs, _ := filepath.Abs(path); abs == absOut || (SourceSkip[d.Name()] && path != root) {
				return filepath.SkipDir
			}
			return nil
		}
		if !d.Type().IsRegular() {
			return nil
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		mode := int64(0o644)
		if fi, err := d.Info(); err == nil && fi.Mode()&0o111 != 0 {
			mode = 0o755
		}
		tf.add("bench/"+filepath.ToSlash(rel), data, mode)
		return nil
	})
	if err != nil {
		return "", err
	}
	data, err := tf.gzip()
	if err != nil {
		return "", err
	}
	path := filepath.Join(out, SourceName(commit))
	return path, writeFile(path, data)
}

var commitRe = regexp.MustCompile(`^[0-9a-f]{7,64}$`)

// SourceName is the source tarball's file name at commit.
func SourceName(commit string) string { return "bench-" + commit + ".tar.gz" }

// Classify says what a built file is from its name.
func Classify(name string) (kind, goos, goarch string) {
	switch {
	case strings.HasPrefix(name, "bifrost-bench-"):
		parts := strings.Split(strings.TrimSuffix(strings.TrimPrefix(name, "bifrost-bench-"), ".exe"), "-")
		if len(parts) == 2 {
			return handlers.DownloadCLI, parts[0], parts[1]
		}
	case strings.HasSuffix(name, ".whl"):
		return handlers.DownloadPythonWheel, "", ""
	case strings.HasPrefix(name, "bench-") && strings.HasSuffix(name, ".tar.gz"):
		return handlers.DownloadSource, "", ""
	case strings.HasSuffix(name, ".tgz"):
		return handlers.DownloadTypeScript, "", ""
	case strings.HasSuffix(name, ".tar.gz"):
		return handlers.DownloadPythonSdist, "", ""
	}
	return "", "", ""
}

// WriteManifest checksums every file Classify knows in dir and writes
// SHA256SUMS and manifest.json beside them. Unknown files are refused, so
// nothing unlisted is ever shipped by accident.
func WriteManifest(dir, commit, version string, builtAt time.Time) (handlers.DownloadsManifest, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return handlers.DownloadsManifest{}, err
	}
	m := handlers.DownloadsManifest{Commit: commit, Version: version, Files: []handlers.DownloadFile{}}
	if !builtAt.IsZero() {
		t := builtAt.UTC()
		m.BuiltAt = &t
	}
	var sums strings.Builder
	for _, e := range entries {
		name := e.Name()
		if name == handlers.DownloadsManifestFile || name == handlers.DownloadsChecksumsFile {
			continue
		}
		if !e.Type().IsRegular() {
			return m, fmt.Errorf("%s: not a regular file", name)
		}
		kind, goos, goarch := Classify(name)
		if kind == "" {
			return m, fmt.Errorf("%s: not a file the server offers (cli, wheel, sdist, npm tarball, source)", name)
		}
		f, err := os.Open(filepath.Join(dir, name))
		if err != nil {
			return m, err
		}
		h := sha256.New()
		n, err := io.Copy(h, f)
		f.Close()
		if err != nil {
			return m, err
		}
		sum := hex.EncodeToString(h.Sum(nil))
		m.Files = append(m.Files, handlers.DownloadFile{Name: name, Kind: kind, OS: goos, Arch: goarch, Size: n, SHA256: sum})
		fmt.Fprintf(&sums, "%s  %s\n", sum, name)
	}
	if len(m.Files) == 0 {
		return m, errors.New("no files to list")
	}
	m.Available = true
	raw, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return m, err
	}
	if err := writeFile(filepath.Join(dir, handlers.DownloadsChecksumsFile), []byte(sums.String())); err != nil {
		return m, err
	}
	return m, writeFile(filepath.Join(dir, handlers.DownloadsManifestFile), append(raw, '\n'))
}

func writeFile(path string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o644)
}
