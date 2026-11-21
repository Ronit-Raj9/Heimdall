// Package researchkit holds what the research importers (gaia,
// browsecompplus) share: the web search tool they install into a suite (a
// verbatim copy of bench/research/websearch, the research track's MCP
// search server), the suite and task YAML they write, and small file
// helpers. docs/importers-research.md describes the suites they write.
package researchkit

import (
	"bytes"
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

// websearchFS is a copy of bench/research/websearch (server.py and its
// Dockerfile); a test fails when it drifts from the original.
//
//go:embed websearch/server.py websearch/Dockerfile
var websearchFS embed.FS

// Names shared by the suites the research importers write.
const (
	// WebsearchService is the id of the MCP search tool service.
	WebsearchService = "websearch"
	// TavilyID is the external API id of Tavily (research.APIID).
	TavilyID = "tavily"
	// BaseImage is the workspace image of an imported research suite: the
	// bench helper image, which has sh and python3 (as acme-research uses).
	BaseImage = "bench/coding-core-base:1"
	// LicenseFile is the licence and terms notice copied into every
	// imported suite.
	LicenseFile = "LICENSE-NOTICE.md"
)

// WebsearchFiles returns the files of the websearch service folder
// (services/websearch/ in a suite) and the image tag that names their
// content, so a changed server builds a new image.
func WebsearchFiles() (map[string][]byte, string, error) {
	files := map[string][]byte{}
	for _, name := range []string{"server.py", "Dockerfile"} {
		b, err := websearchFS.ReadFile("websearch/" + name)
		if err != nil {
			return nil, "", fmt.Errorf("embedded websearch %s: %w", name, err)
		}
		files[name] = b
	}
	return files, "bench/import-websearch:" + ContentTag(files), nil
}

// ContentTag is the first 12 hex digits of the sha256 over the files' names
// and contents, in name order.
func ContentTag(files map[string][]byte) string {
	names := make([]string, 0, len(files))
	for n := range files {
		names = append(names, n)
	}
	sort.Strings(names)
	h := sha256.New()
	for _, n := range names {
		fmt.Fprintf(h, "%s\x00%d\x00", n, len(files[n]))
		h.Write(files[n])
	}
	return hex.EncodeToString(h.Sum(nil))[:12]
}

// Service is a service entry of a suite or task, as the suite schema reads
// it (schemas.ServiceSpec), with every optional field omitted when empty.
type Service struct {
	ID    string            `yaml:"id"`
	Role  string            `yaml:"role"`
	Image string            `yaml:"image,omitempty"`
	Build *Build            `yaml:"build,omitempty"`
	Argv  []string          `yaml:"argv,omitempty,flow"`
	Port  int               `yaml:"port"`
	MCP   *MCP              `yaml:"mcp,omitempty"`
	Env   map[string]string `yaml:"env,omitempty"`
	Files map[string]string `yaml:"files,omitempty"`
	Ready *Ready            `yaml:"ready,omitempty"`
}

// Build names a service image's Dockerfile and tag.
type Build struct {
	Dockerfile string `yaml:"dockerfile"`
	Tag        string `yaml:"tag"`
}

// MCP is an MCP endpoint.
type MCP struct {
	Path string `yaml:"path"`
}

// Ready is a readiness check.
type Ready struct {
	Argv     []string `yaml:"argv,flow"`
	TimeoutS int      `yaml:"timeout_s"`
}

// External is an external API entry (schemas.ExternalAPI).
type External struct {
	ID         string            `yaml:"id"`
	BaseURL    string            `yaml:"base_url"`
	Allow      []Allow           `yaml:"allow"`
	Headers    map[string]string `yaml:"headers,omitempty"`
	AuthEnv    string            `yaml:"auth_env,omitempty"`
	AuthHeader string            `yaml:"auth_header,omitempty"`
	AuthScheme string            `yaml:"auth_scheme,omitempty"`
	Mode       string            `yaml:"mode,omitempty"`
	Cassette   string            `yaml:"cassette"`
	MaxCalls   int               `yaml:"max_calls"`
	MaxBytes   int64             `yaml:"max_bytes"`
	TimeoutS   int               `yaml:"timeout_s"`
}

// Allow is one allowlist entry.
type Allow struct {
	Methods    []string `yaml:"methods,flow"`
	PathPrefix string   `yaml:"path_prefix"`
}

// WebsearchServiceSpec is the websearch MCP service: it calls tavilyURL, the
// tool proxy route of the search backend (the Tavily route
// http://toolproxy:9200/x/tavily, or a corpus service under /s/).
func WebsearchServiceSpec(tag, tavilyURL string) Service {
	return Service{
		ID: WebsearchService, Role: "mcp", Image: tag, Port: 8000, MCP: &MCP{Path: "/mcp"},
		Env:   map[string]string{"TAVILY_URL": tavilyURL},
		Ready: &Ready{Argv: []string{"python3", "-I", "/srv/server.py", "ready"}, TimeoutS: 30},
	}
}

// TavilyReplay is the Tavily external API of a research task, replayed from
// cassette (relative to the task or suite folder). The key settings only
// matter in live and record mode, which an experiment must opt into.
func TavilyReplay(cassette string) External {
	return External{
		ID: TavilyID, BaseURL: "https://api.tavily.com",
		Allow:   []Allow{{Methods: []string{"POST"}, PathPrefix: "/search"}, {Methods: []string{"POST"}, PathPrefix: "/extract"}},
		Headers: map[string]string{"Content-Type": "application/json"},
		AuthEnv: "TAVILY_API_KEY", AuthHeader: "Authorization", AuthScheme: "Bearer",
		Mode: "replay", Cassette: cassette, MaxCalls: 60, MaxBytes: 262144, TimeoutS: 30,
	}
}

// MarshalYAML encodes v with a two-space indent.
func MarshalYAML(v any) ([]byte, error) {
	var buf bytes.Buffer
	enc := yaml.NewEncoder(&buf)
	enc.SetIndent(2)
	if err := enc.Encode(v); err != nil {
		return nil, fmt.Errorf("encode yaml: %w", err)
	}
	if err := enc.Close(); err != nil {
		return nil, fmt.Errorf("encode yaml: %w", err)
	}
	return buf.Bytes(), nil
}

// WriteTree writes files (paths relative to dir, slash-separated) under
// dir, creating folders. Paths that would leave dir are refused.
func WriteTree(dir string, files map[string][]byte) error {
	names := make([]string, 0, len(files))
	for n := range files {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, rel := range names {
		clean := filepath.Clean(filepath.FromSlash(rel))
		if filepath.IsAbs(clean) || clean == "." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) || clean == ".." {
			return fmt.Errorf("refusing to write %q outside %s", rel, dir)
		}
		p := filepath.Join(dir, clean)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			return err
		}
		mode := os.FileMode(0o644)
		if strings.HasSuffix(rel, ".sh") {
			mode = 0o755
		}
		if err := os.WriteFile(p, files[rel], mode); err != nil {
			return err
		}
	}
	return nil
}

// PrepareTaskDir makes room for a task folder: an existing one is removed
// when force is set and is otherwise reported as fs.ErrExist (the task is
// skipped, so a re-import without -force keeps what is there).
func PrepareTaskDir(dir string, force bool) error {
	if _, err := os.Stat(dir); err == nil {
		if !force {
			return fmt.Errorf("task folder %s: %w (use -force to replace it)", dir, fs.ErrExist)
		}
		if err := os.RemoveAll(dir); err != nil {
			return fmt.Errorf("replace task folder %s: %w", dir, err)
		}
	} else if !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	return nil
}

// idNotAllowed is every run of characters a task id cannot hold.
var idNotAllowed = regexp.MustCompile(`[^a-z0-9.-]+`)

// TaskID turns an upstream id into a task id: lower case, every run of
// characters outside [a-z0-9.-] turned into one '-', no leading or trailing
// '-' or '.'.
func TaskID(prefix, upstream string) string {
	id := strings.Trim(idNotAllowed.ReplaceAllString(strings.ToLower(upstream), "-"), "-.")
	if prefix != "" {
		id = prefix + "-" + id
	}
	return id
}

// MergeIDs appends to have the ids of add it lacks, keeping order.
func MergeIDs(have, add []string) []string {
	out := append([]string(nil), have...)
	seen := map[string]bool{}
	for _, id := range have {
		seen[id] = true
	}
	for _, id := range add {
		if !seen[id] {
			seen[id] = true
			out = append(out, id)
		}
	}
	return out
}

// ShellQuote quotes s for sh as one word.
func ShellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}
