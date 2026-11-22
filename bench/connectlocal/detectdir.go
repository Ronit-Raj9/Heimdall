package connectlocal

import (
	"encoding/json"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// Finding is one way to serve an agent found in a repository.
type Finding struct {
	// Framework is langgraph, crewai, openai-agents, pydantic-ai, adk,
	// smolagents, dspy, function, mastra or http.
	Framework string `json:"framework"`
	// Object is what `heimdall serve` loads: path/to/file.py:name.
	Object     string   `json:"object,omitempty"`
	Language   string   `json:"language"`
	Confidence string   `json:"confidence"` // high | medium | low
	Evidence   []string `json:"evidence"`
	// Serve is the command that serves it to Heimdall.
	Serve string `json:"serve,omitempty"`
	// Note says what is left to do by hand, if anything.
	Note string `json:"note,omitempty"`
}

// Detection is what DetectDir found.
type Detection struct {
	Root     string    `json:"root"`
	Findings []Finding `json:"findings"`
	// Packages are the agent frameworks named in the dependency files.
	Packages []string `json:"packages,omitempty"`
}

// Best is the most likely finding, or nil.
func (d *Detection) Best() *Finding {
	if len(d.Findings) == 0 {
		return nil
	}
	return &d.Findings[0]
}

// Limits keep detection a quick read of the files, never a crawl.
const (
	detectMaxFiles = 600
	detectMaxBytes = 256 << 10
	detectMaxDepth = 5
)

var skipDirs = map[string]bool{".git": true, "node_modules": true, ".venv": true, "venv": true, "env": true, "__pycache__": true,
	"site-packages": true, "dist": true, "build": true, ".tox": true, ".mypy_cache": true, ".pytest_cache": true, ".heimdall": true, "tests": true, "test": true}

// depPackages maps a dependency name to its framework.
var depPackages = map[string]string{
	"langgraph": "langgraph", "crewai": "crewai", "openai-agents": "openai-agents", "pydantic-ai": "pydantic-ai",
	"pydantic-ai-slim": "pydantic-ai", "google-adk": "adk", "smolagents": "smolagents", "dspy": "dspy", "dspy-ai": "dspy",
	"@mastra/core": "mastra", "fastapi": "http", "flask": "http",
}

type objRule struct {
	framework  string
	needImport *regexp.Regexp // the file must import the framework
	assign     *regexp.Regexp // group 1 is the object's name
	confidence string
}

var objRules = []objRule{
	{"adk", regexp.MustCompile(`(?m)^\s*from\s+google\.adk|^\s*import\s+google\.adk`), regexp.MustCompile(`(?m)^(root_agent)\s*[:=]`), "high"},
	{"langgraph", regexp.MustCompile(`(?m)^\s*from\s+langgraph|^\s*import\s+langgraph`), regexp.MustCompile(`(?m)^([A-Za-z_][A-Za-z0-9_]*)\s*=\s*[A-Za-z_][A-Za-z0-9_.]*\.compile\(`), "high"},
	{"langgraph", regexp.MustCompile(`(?m)^\s*from\s+langgraph\.prebuilt`), regexp.MustCompile(`(?m)^([A-Za-z_][A-Za-z0-9_]*)\s*=\s*create_react_agent\(`), "high"},
	{"crewai", regexp.MustCompile(`(?m)^\s*from\s+crewai|^\s*import\s+crewai`), regexp.MustCompile(`(?m)^([A-Za-z_][A-Za-z0-9_]*)\s*=\s*Crew\(`), "high"},
	{"crewai", regexp.MustCompile(`(?m)^\s*from\s+crewai\.project`), regexp.MustCompile(`(?m)^@CrewBase\s*\n\s*class\s+([A-Za-z_][A-Za-z0-9_]*)`), "medium"},
	{"openai-agents", regexp.MustCompile(`(?m)^\s*from\s+agents\s+import`), regexp.MustCompile(`(?m)^([A-Za-z_][A-Za-z0-9_]*)\s*=\s*Agent(?:\[[^\]]*\])?\(`), "high"},
	{"pydantic-ai", regexp.MustCompile(`(?m)^\s*from\s+pydantic_ai\s+import`), regexp.MustCompile(`(?m)^([A-Za-z_][A-Za-z0-9_]*)\s*=\s*Agent(?:\[[^\]]*\])?\(`), "high"},
	{"smolagents", regexp.MustCompile(`(?m)^\s*from\s+smolagents|^\s*import\s+smolagents`), regexp.MustCompile(`(?m)^([A-Za-z_][A-Za-z0-9_]*)\s*=\s*(?:CodeAgent|ToolCallingAgent|MultiStepAgent)\(`), "high"},
	{"dspy", regexp.MustCompile(`(?m)^\s*import\s+dspy|^\s*from\s+dspy`), regexp.MustCompile(`(?m)^([A-Za-z_][A-Za-z0-9_]*)\s*=\s*dspy\.(?:Predict|ChainOfThought|ReAct|ProgramOfThought|Refine)\(`), "high"},
	{"dspy", regexp.MustCompile(`(?m)^\s*import\s+dspy|^\s*from\s+dspy`), regexp.MustCompile(`(?m)^([A-Za-z_][A-Za-z0-9_]*)\s*=\s*[A-Z][A-Za-z0-9_]*\(\)\s*$`), "low"},
}

var (
	fastAPIRE = regexp.MustCompile(`(?m)^([A-Za-z_][A-Za-z0-9_]*)\s*=\s*(?:FastAPI|Flask)\(`)
	plainFnRE = regexp.MustCompile(`(?m)^(?:async\s+)?def\s+(agent|run_agent|answer|respond|chat|solve|handle|run)\s*\(`)
)

// DetectDir reads a repository and proposes how to serve its agent with
// `heimdall serve`. It only reads files; nothing in the repository runs.
func DetectDir(root string) (*Detection, error) {
	abs, err := filepath.Abs(root)
	if err != nil {
		return nil, err
	}
	d := &Detection{Root: abs}
	seenPkg := map[string]bool{}
	addPkgs := func(text string) {
		low := strings.ToLower(text)
		for name, fw := range depPackages {
			re := regexp.MustCompile(`(?m)(^|[\s"'\[,])` + regexp.QuoteMeta(name) + `($|[\s"'\],<>=~!;\[@])`)
			if re.MatchString(low) && !seenPkg[fw] {
				seenPkg[fw] = true
				d.Packages = append(d.Packages, fw)
			}
		}
	}
	for _, f := range []string{"pyproject.toml", "requirements.txt", "requirements.in", "setup.py", "setup.cfg", "Pipfile", "package.json"} {
		if b, err := readCapped(filepath.Join(abs, f)); err == nil {
			addPkgs(string(b))
		}
	}
	sort.Strings(d.Packages)

	// langgraph.json names the graphs exactly.
	if b, err := readCapped(filepath.Join(abs, "langgraph.json")); err == nil {
		var lg struct {
			Graphs map[string]any `json:"graphs"`
		}
		if json.Unmarshal(b, &lg) == nil {
			names := make([]string, 0, len(lg.Graphs))
			for k := range lg.Graphs {
				names = append(names, k)
			}
			sort.Strings(names)
			for _, name := range names {
				ref := ""
				switch v := lg.Graphs[name].(type) {
				case string:
					ref = v
				case map[string]any:
					ref, _ = v["path"].(string)
				}
				if ref == "" {
					continue
				}
				ref = strings.TrimPrefix(ref, "./")
				f := Finding{Framework: "langgraph", Object: ref, Language: "python", Confidence: "high",
					Evidence: []string{"langgraph.json graphs." + name + " = " + ref}}
				if file, _, _ := strings.Cut(ref, ":"); strings.HasSuffix(file, ".ts") || strings.HasSuffix(file, ".js") {
					f.Language = "typescript"
					f.Note = "a TypeScript graph: serve it with the TypeScript SDK (serve from @heimdall/bench-client) or run `langgraph dev` and connect its URL"
				}
				d.Findings = append(d.Findings, f)
			}
		}
	}

	files := 0
	seenObj := map[string]bool{}
	for _, f := range d.Findings {
		seenObj[f.Object] = true
	}
	var httpApps, fns []Finding
	_ = filepath.WalkDir(abs, func(p string, e fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		rel, _ := filepath.Rel(abs, p)
		if e.IsDir() {
			if p != abs && (skipDirs[e.Name()] || strings.HasPrefix(e.Name(), ".") || strings.Count(rel, string(os.PathSeparator)) >= detectMaxDepth) {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(p, ".py") || files >= detectMaxFiles {
			return nil
		}
		files++
		b, err := readCapped(p)
		if err != nil {
			return nil
		}
		text := string(b)
		rel = filepath.ToSlash(rel)
		for _, r := range objRules {
			if !r.needImport.MatchString(text) {
				continue
			}
			for _, m := range r.assign.FindAllStringSubmatch(text, 3) {
				ref := rel + ":" + m[1]
				if seenObj[ref] {
					continue
				}
				seenObj[ref] = true
				d.Findings = append(d.Findings, Finding{Framework: r.framework, Object: ref, Language: "python", Confidence: r.confidence,
					Evidence: []string{rel + " imports " + r.framework + " and defines " + m[1]}})
			}
		}
		if m := fastAPIRE.FindStringSubmatch(text); m != nil {
			httpApps = append(httpApps, Finding{Framework: "http", Language: "python", Confidence: "low",
				Evidence: []string{rel + " defines a web app (" + m[1] + ")"},
				Note:     "a web service: start it, then `heimdall connect http://localhost:<port>` (or `heimdall serve http://localhost:<port> --map auto` to relay it)"})
		}
		if base := filepath.Base(p); base == "agent.py" || base == "main.py" || base == "app.py" || base == "bot.py" {
			if m := plainFnRE.FindStringSubmatch(text); m != nil && !seenObj[rel+":"+m[1]] {
				seenObj[rel+":"+m[1]] = true
				fns = append(fns, Finding{Framework: "function", Object: rel + ":" + m[1], Language: "python", Confidence: "low",
					Evidence: []string{rel + " defines " + m[1] + "()"}, Note: "a plain function: it is called with the task text (or messages, or the turn) and returns the answer"})
			}
		}
		return nil
	})
	d.Findings = append(d.Findings, fns...)
	d.Findings = append(d.Findings, httpApps...)
	if seenPkg["mastra"] {
		d.Findings = append(d.Findings, Finding{Framework: "mastra", Language: "typescript", Confidence: "medium",
			Evidence: []string{"package.json depends on @mastra/core"},
			Note:     "a Mastra app: run `mastra dev` and connect its URL, or serve the agent with the TypeScript SDK"})
	}
	rank := map[string]int{"high": 0, "medium": 1, "low": 2}
	sort.SliceStable(d.Findings, func(i, j int) bool {
		a, b := d.Findings[i], d.Findings[j]
		if rank[a.Confidence] != rank[b.Confidence] {
			return rank[a.Confidence] < rank[b.Confidence]
		}
		// A framework named in the dependencies wins over one only imported.
		return seenPkg[a.Framework] && !seenPkg[b.Framework]
	})
	for i := range d.Findings {
		if d.Findings[i].Object != "" && d.Findings[i].Language == "python" {
			d.Findings[i].Serve = "heimdall serve " + d.Findings[i].Object
		}
	}
	return d, nil
}

func readCapped(p string) ([]byte, error) {
	f, err := os.Open(p)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	return io.ReadAll(io.LimitReader(f, detectMaxBytes))
}
