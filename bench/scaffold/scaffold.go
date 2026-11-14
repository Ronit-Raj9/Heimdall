// Package scaffold writes the files of a new Heimdall project, suite or task:
// `bifrost-bench init`, `suite init` and `task init`.
//
// Every file a fresh project needs and that otherwise lives only in the bench
// repository (the gateway stack, the pricing snapshot and Bifrost's
// datasheets, the control agents, the default policy, the suite toolchain
// images) is embedded here, so a project works with only the binary and
// Docker. The generators are pure: they return Files and never touch the
// disk. WriteFiles and InitTask, which reads a git repository with plumbing
// commands, are the only functions that do I/O.
//
// Template files follow three naming rules, so that nothing under
// templates/ is mistaken for something else by git or the Go tool: a path
// segment starting with "dot-" is written with a leading "." instead, a
// ".tmpl" file is rendered with text/template (delimiters [[ ]]) and loses
// the suffix, a ".raw" file loses the suffix unrendered, and a ".gz" file is
// written decompressed without the suffix.
package scaffold

import (
	"bytes"
	"compress/gzip"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"text/template"
)

//go:embed all:templates
var templates embed.FS

// File is one file to write, relative to the folder it is written into.
type File struct {
	Path string      // slash-separated and relative
	Data []byte      // content; empty for a symlink
	Mode fs.FileMode // permission bits: 0o644, or 0o755 for scripts
	Link string      // symlink target when set
}

// Suite template names (`suite init -template`).
const (
	TemplatePythonPytest  = "python-pytest"
	TemplateNodeTest      = "node-test"
	TemplateGoTest        = "go-test"
	TemplateGenericReward = "generic-reward"
)

// Grader part names the suite templates select.
const (
	graderHiddenTests     = "hidden_tests"
	graderVerifierCommand = "verifier_command"
)

// idRE is the id pattern of spec/suite.schema.json and spec/task.schema.json.
var idRE = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]*$`)

// SuiteTemplates lists the suite templates in a stable order.
func SuiteTemplates() []string {
	return []string{TemplatePythonPytest, TemplateNodeTest, TemplateGoTest, TemplateGenericReward}
}

// ValidID reports whether id is usable as a suite or task id (and folder).
func ValidID(id string) bool { return idRE.MatchString(id) }

// ProjectOptions configures Project.
type ProjectOptions struct {
	Name string // shown in the README; the folder name by default
}

// Project returns every file of a new project folder: deploy/ (bench.yaml
// with relative paths, the compose stack, .env.example, the bench and helper
// image Dockerfiles, the pricing snapshot and Bifrost's config and
// datasheets), the control agents, an empty benchmarks/ with a README, a
// smoke experiment, policies/default.yaml, .gitignore and README.md.
func Project(opts ProjectOptions) ([]File, error) {
	name := strings.TrimSpace(opts.Name)
	if name == "" {
		name = "heimdall"
	}
	return renderTree("templates/project", map[string]any{"Name": name})
}

// ProjectDefault returns the embedded default of one project file, by its
// path in a project ("deploy/pricing-snapshot.json", "policies/default.yaml",
// "agents/oracle.yaml"), so a command can fall back to it when a project
// lacks the file.
func ProjectDefault(rel string) ([]byte, error) {
	files, err := Project(ProjectOptions{})
	if err != nil {
		return nil, err
	}
	for _, f := range files {
		if f.Path == rel {
			return f.Data, nil
		}
	}
	return nil, fmt.Errorf("no embedded default for %s", rel)
}

// SuiteOptions configures Suite.
type SuiteOptions struct {
	ID       string
	Template string
	// Image is an existing toolchain image to use instead of building
	// base/Dockerfile; no Dockerfile is written then. Empty builds
	// bench/<id>-base:1 from base/Dockerfile.
	Image string
}

// suiteAbout is the template-specific paragraph of a suite README.
var suiteAbout = map[string]string{
	TemplatePythonPytest: "Each task's hidden pytest files run with the `hidden_tests` grader. pytest runs in the\n" +
		"helper image (`images.helper` in deploy/bench.yaml), so keep it and base/Dockerfile in step.\n" +
		"Required test ids are `path/test_file.py::test_name`.",
	TemplateNodeTest: "Each task's hidden node:test files run with the `verifier_command` grader and report JUnit\n" +
		"XML. Node's JUnit reporter names every test case with classname `test`, so required test ids\n" +
		"are `test::<test name>`.",
	TemplateGoTest: "Each task's hidden _test.go files run with `go test -json ./...` from a copy of the workspace,\n" +
		"converted to JUnit XML by go-junit (base/gojunit). Required test ids are\n" +
		"`<import path>::<TestName>`.",
	TemplateGenericReward: "Each task's verifier/tests/test.sh decides and writes a reward to /grader/out/reward.json\n" +
		"(the Harbor task shape). A reward of at least `pass_threshold` passes.",
}

// Suite returns the files of a new suite folder from a template: suite.yaml,
// base/Dockerfile (unless opts.Image names an existing image), README.md and
// the model-outage probe. The suite starts with no tasks.
func Suite(opts SuiteOptions) ([]File, error) {
	if !ValidID(opts.ID) {
		return nil, fmt.Errorf("suite id %q: want lowercase letters, digits, '.', '_' or '-'", opts.ID)
	}
	about, ok := suiteAbout[opts.Template]
	if !ok {
		return nil, fmt.Errorf("unknown suite template %q (want %s)", opts.Template, strings.Join(SuiteTemplates(), ", "))
	}
	image := strings.TrimSpace(opts.Image)
	var build string
	if image == "" {
		image = "bench/" + opts.ID + "-base:1"
		build = "docker build -t " + image + " benchmarks/" + opts.ID + "/base"
	} else {
		build = "# base image " + image + ": uid 1000 user, git, /workspace and /grader/out owned by it"
	}
	data := map[string]any{
		"ID": opts.ID, "Template": opts.Template, "Image": image,
		"Dockerfile": strings.TrimSpace(opts.Image) == "", "About": about, "Build": build,
	}
	files, err := renderTree("templates/suites/"+opts.Template, data)
	if err != nil {
		return nil, err
	}
	common, err := renderTree("templates/suites/common", data)
	if err != nil {
		return nil, err
	}
	files = append(files, common...)
	if strings.TrimSpace(opts.Image) != "" {
		kept := files[:0]
		for _, f := range files {
			if !strings.HasPrefix(f.Path, "base/") {
				kept = append(kept, f)
			}
		}
		files = kept
	}
	sortFiles(files)
	return files, nil
}

// WriteFiles writes files under dir, creating folders as needed. An existing
// file is an error unless overwrite is set.
func WriteFiles(dir string, files []File, overwrite bool) error {
	for _, f := range files {
		if err := checkRel(f.Path); err != nil {
			return fmt.Errorf("write %s: %w", f.Path, err)
		}
		dst := filepath.Join(dir, filepath.FromSlash(f.Path))
		if _, err := os.Lstat(dst); err == nil {
			if !overwrite {
				return fmt.Errorf("write %s: file exists", dst)
			}
			if err := os.Remove(dst); err != nil {
				return fmt.Errorf("write %s: %w", dst, err)
			}
		}
		if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
			return fmt.Errorf("write %s: %w", dst, err)
		}
		if f.Link != "" {
			if err := os.Symlink(f.Link, dst); err != nil {
				return fmt.Errorf("write %s: %w", dst, err)
			}
			continue
		}
		mode := f.Mode
		if mode == 0 {
			mode = 0o644
		}
		if err := os.WriteFile(dst, f.Data, mode); err != nil {
			return fmt.Errorf("write %s: %w", dst, err)
		}
		// WriteFile applies the umask; scripts must stay executable.
		if err := os.Chmod(dst, mode); err != nil {
			return fmt.Errorf("write %s: %w", dst, err)
		}
	}
	return nil
}

// renderTree turns every embedded file under root into a File, applying the
// naming rules in the package comment.
func renderTree(root string, data any) ([]File, error) {
	var files []File
	err := fs.WalkDir(templates, root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		raw, err := templates.ReadFile(p)
		if err != nil {
			return err
		}
		rel := strings.TrimPrefix(p, root+"/")
		f, err := renderFile(rel, raw, data)
		if err != nil {
			return fmt.Errorf("template %s: %w", p, err)
		}
		files = append(files, f)
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("render %s: %w", root, err)
	}
	sortFiles(files)
	return files, nil
}

// renderFile applies the naming rules to one template file.
func renderFile(rel string, raw []byte, data any) (File, error) {
	parts := strings.Split(rel, "/")
	for i, s := range parts {
		if strings.HasPrefix(s, "dot-") {
			parts[i] = "." + strings.TrimPrefix(s, "dot-")
		}
	}
	out := strings.Join(parts, "/")
	body := raw
	switch {
	case strings.HasSuffix(out, ".tmpl"):
		out = strings.TrimSuffix(out, ".tmpl")
		b, err := render(rel, string(raw), data)
		if err != nil {
			return File{}, err
		}
		body = b
	case strings.HasSuffix(out, ".raw"):
		out = strings.TrimSuffix(out, ".raw")
	case strings.HasSuffix(out, ".gz"):
		out = strings.TrimSuffix(out, ".gz")
		b, err := gunzip(raw)
		if err != nil {
			return File{}, err
		}
		body = b
	}
	mode := fs.FileMode(0o644)
	if strings.HasSuffix(out, ".sh") {
		mode = 0o755
	}
	return File{Path: out, Data: body, Mode: mode}, nil
}

// render executes one text/template with [[ ]] delimiters, so files that use
// {{ }} themselves (agent configs, compose) need no escaping.
func render(name, text string, data any) ([]byte, error) {
	t, err := template.New(name).Delims("[[", "]]").Option("missingkey=error").Funcs(template.FuncMap{
		"q":    quote,
		"flow": flowList,
	}).Parse(text)
	if err != nil {
		return nil, err
	}
	var buf bytes.Buffer
	if err := t.Execute(&buf, data); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// quote renders s as a double-quoted scalar, valid YAML because it is valid
// JSON.
func quote(s string) string {
	b, err := json.Marshal(s)
	if err != nil {
		return `""`
	}
	return string(b)
}

// flowList renders a YAML flow sequence of quoted strings.
func flowList(items []string) string {
	q := make([]string, len(items))
	for i, s := range items {
		q[i] = quote(s)
	}
	return "[" + strings.Join(q, ", ") + "]"
}

func gunzip(b []byte) ([]byte, error) {
	zr, err := gzip.NewReader(bytes.NewReader(b))
	if err != nil {
		return nil, fmt.Errorf("gunzip: %w", err)
	}
	defer zr.Close()
	out, err := io.ReadAll(zr)
	if err != nil {
		return nil, fmt.Errorf("gunzip: %w", err)
	}
	return out, nil
}

func sortFiles(files []File) {
	sort.Slice(files, func(i, j int) bool { return files[i].Path < files[j].Path })
}

// checkRel refuses paths that are empty, absolute, or leave the folder.
func checkRel(p string) error {
	if p == "" || path.IsAbs(p) || strings.HasPrefix(p, "/") {
		return errors.New("want a relative path")
	}
	c := path.Clean(p)
	if c != p || c == "." || c == ".." || strings.HasPrefix(c, "../") {
		return errors.New("path leaves the folder or is not clean")
	}
	return nil
}
