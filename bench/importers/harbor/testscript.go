package harbor

import (
	"fmt"
	"regexp"
	"strings"
)

// Where baked verifier tools live in the task image. Fixed paths, not $HOME,
// so the verifier finds them whatever user it runs as.
const (
	toolsRoot     = "/opt/harbor-tests"
	uvInstallDir  = toolsRoot + "/uv"
	uvPythonDir   = toolsRoot + "/python"
	bakeMarker    = "baked into the task image at build by bifrost-bench import harbor"
	offlineHeader = "# Changed by bifrost-bench import harbor: the bench verifier has no network,\n" +
		"# so the lines of the upstream script that downloaded tools now run when the\n" +
		"# task image is built (see the end of environment/Dockerfile) and are\n" +
		"# disabled here. Everything else is unchanged.\n"
)

// testPlan is how one upstream test.sh runs without network: the commands
// moved to the image build, the rewritten script, and what changed.
type testPlan struct {
	Bake    []string // shell commands for the image build, in script order
	Script  string   // test.sh with the baked lines disabled or rewritten
	Changes []string // one line per change, for the task's IMPORT.md
}

// logicalLine is one shell line with its backslash continuations joined.
type logicalLine struct {
	first, last int    // physical line indexes it spans
	text        string // joined text, trimmed
}

// planTestScript converts test.sh so it runs with no network. Lines that
// install packages (apt-get, pip) move to the image build unchanged; the uv
// installer moves there with a fixed install folder; each uvx line becomes a
// prepared virtual environment holding the same packages, called by absolute
// path. Any other line that would download something makes the task
// unsupported, and the error quotes it: running it offline would fail the
// reference for a reason that is not the agent's.
func planTestScript(src string) (*testPlan, error) {
	phys := strings.Split(strings.ReplaceAll(src, "\r\n", "\n"), "\n")
	plan := &testPlan{}
	out := make([]string, 0, len(phys)+4)
	uvInstalled, venvs := false, 0
	for _, ll := range logicalLines(phys) {
		orig := phys[ll.first : ll.last+1]
		code := stripComment(ll.text)
		switch kind := classifyTestLine(code); kind {
		case lineKeep:
			out = append(out, orig...)
		case lineAptOrPip:
			plan.Bake = append(plan.Bake, code)
			plan.Changes = append(plan.Changes, "baked at build: "+code)
			out = append(out, disabled(ll.text))
		case lineUVInstall:
			url := uvURLRE.FindString(code)
			plan.Bake = append(plan.Bake, fmt.Sprintf("curl -LsSf %s | env UV_UNMANAGED_INSTALL=%s sh", url, uvInstallDir))
			plan.Changes = append(plan.Changes, fmt.Sprintf("baked at build: uv from %s, installed in %s", url, uvInstallDir))
			uvInstalled = true
			out = append(out, disabled(ll.text))
		case lineUVEnv:
			out = append(out, disabled(ll.text))
		case lineUVX:
			venvs++
			step, line, err := planUVX(code, venvs, uvInstalled)
			if err != nil {
				return nil, err
			}
			plan.Bake = append(plan.Bake, step)
			plan.Changes = append(plan.Changes, fmt.Sprintf("uvx replaced by a virtual environment built at image build: %s", short(code)))
			out = append(out, "# "+bakeMarker+": "+short(ll.text), line)
		default:
			return nil, fmt.Errorf("test.sh fetches from the network at verify time in a way the importer cannot move to the image build (%s): %s", kind, short(code))
		}
	}
	plan.Script = withHeader(strings.Join(out, "\n"), offlineHeader)
	return plan, nil
}

// Line kinds of a test script. The unsupported kinds name the reason.
const (
	lineKeep      = "keep"
	lineAptOrPip  = "package install"
	lineUVInstall = "uv installer"
	lineUVEnv     = "uv env"
	lineUVX       = "uvx"
)

var (
	aptSegRE    = regexp.MustCompile(`^(export\s+)?(DEBIAN_FRONTEND=\S+\s*)?((sudo\s+)?(apt-get|apt)\s+(-\S+\s+)*(update|install)\b.*|rm -rf /var/lib/apt/lists/\*)?$`)
	pipSegRE    = regexp.MustCompile(`^(python3?\s+-m\s+)?pip3?\s+install\s+.*$`)
	uvURLRE     = regexp.MustCompile(`https://astral\.sh/uv/([0-9][0-9.]*/)?install\.sh`)
	uvInstallRE = regexp.MustCompile(`^(curl|wget)\s.*https://astral\.sh/uv/([0-9][0-9.]*/)?install\.sh.*\|\s*(sh|bash)\b`)
	uvEnvRE     = regexp.MustCompile(`^(source|\.)\s+"?\$(HOME|\{HOME\})/\.(local/bin|cargo)/env"?$`)
	redirectRE  = regexp.MustCompile(`\s*(\d?>>?\s*/dev/null|\d?>&\d|\|\|\s*true)\s*`)
	uvxRE       = regexp.MustCompile(`(^|[\s;(&|!])uvx\s`)
	uvProjectRE = regexp.MustCompile(`(^|[\s;&|])uv\s+(init|add|run|sync|venv|pip|python|tool)\b`)
	urlRE       = regexp.MustCompile(`https?://([^/\s"'$]+)`)
	fetchRE     = regexp.MustCompile(`(^|[\s;&|(])(` +
		`(apt-get|apt|apk|yum|dnf|conda|mamba|gem|brew)\s+(-\S+\s+)*(install|add|update)\b|` +
		`pip3?\s+(install|download)\b|` +
		`git\s+(clone|fetch|pull|submodule)\b|` +
		`(npm|pnpm|yarn)\s+(install|ci|i|add)\b|npx\s|` +
		`cargo\s+(install|fetch)\b|go\s+(get|install|mod\s+download)\b|` +
		`(huggingface-cli|hf)\s+download\b)`)
)

// classifyTestLine says what one logical line of test.sh is.
func classifyTestLine(code string) string {
	switch {
	case code == "":
		return lineKeep
	case uvInstallRE.MatchString(code):
		return lineUVInstall
	case uvEnvRE.MatchString(code):
		return lineUVEnv
	case uvxRE.MatchString(code):
		return lineUVX
	case uvProjectRE.MatchString(code):
		return "uv project commands"
	case allSegments(code, aptSegRE, pipSegRE):
		return lineAptOrPip
	case fetchRE.MatchString(code):
		return "package or source download"
	}
	for _, m := range urlRE.FindAllStringSubmatch(code, -1) {
		if !isLocalHost(m[1]) && (strings.Contains(code, "curl") || strings.Contains(code, "wget")) {
			return "download from " + m[1]
		}
	}
	return lineKeep
}

// allSegments reports whether code is one or more apt or pip commands joined
// by && , || or ;, with output redirections and `|| true` allowed, and at
// least one real install. Anything else on the line (a condition, a pipe, a
// reference to the test files) keeps it from being moved to the build.
func allSegments(code string, res ...*regexp.Regexp) bool {
	if strings.Contains(code, "/tests") || strings.Contains(code, "/verifier") || strings.Contains(code, "$") || strings.Contains(code, "`") {
		return false
	}
	clean := redirectRE.ReplaceAllString(code, " ")
	segs := regexp.MustCompile(`\s*(&&|\|\||;)\s*`).Split(strings.TrimSpace(clean), -1)
	real := false
	for _, s := range segs {
		s = strings.TrimSpace(s)
		if s == "" || s == "true" {
			continue
		}
		matched := false
		for _, re := range res {
			if re.MatchString(s) {
				matched = true
			}
		}
		if !matched {
			return false
		}
		if strings.Contains(s, "apt") || strings.Contains(s, "pip") {
			real = true
		}
	}
	return real
}

// planUVX turns one uvx line into a build step that prepares a virtual
// environment with the same Python and packages, and the same line calling
// the command from that environment by absolute path. The step uses the uv
// the script installed, else the uv already on the image's PATH (some task
// images ship it); a build without either fails, and admission reports it.
func planUVX(code string, n int, uvInstalled bool) (string, string, error) {
	loc := uvxRE.FindStringIndex(code)
	start := loc[0]
	if code[start] != 'u' {
		start++
	}
	prefix, rest := code[:start], strings.Fields(code[start+len("uvx"):])
	var python, from string
	var with, index []string // index: package index options, passed on to uv pip install
	i := 0
	for ; i < len(rest); i++ {
		tok := rest[i]
		if !strings.HasPrefix(tok, "-") {
			break
		}
		name, val, hasVal := strings.Cut(tok, "=")
		isIndex := uvxIndexOptions[name]
		takesValue := isIndex || name == "-p" || name == "--python" || name == "-w" || name == "--with" || name == "--from"
		switch {
		case name == "-q" || name == "--quiet" || name == "--isolated" || name == "--no-progress":
			continue
		case !takesValue:
			return "", "", fmt.Errorf("test.sh uses uvx option %s, which the importer does not know: %s", name, short(code))
		case !hasVal:
			if i+1 >= len(rest) {
				return "", "", fmt.Errorf("test.sh: uvx option %s has no value: %s", name, short(code))
			}
			i++
			val = rest[i]
		}
		switch name {
		case "-p", "--python":
			python = val
		case "-w", "--with":
			with = append(with, val)
		case "--from":
			from = val
		}
		if isIndex {
			index = append(index, name, val)
		}
	}
	if i >= len(rest) {
		return "", "", fmt.Errorf("test.sh: uvx line names no command: %s", short(code))
	}
	cmd := rest[i]
	for _, v := range append(append([]string{python, from, cmd}, with...), index...) {
		if strings.ContainsAny(v, "$`\"'") {
			return "", "", fmt.Errorf("test.sh: uvx line uses shell variables or quotes, which are only known when it runs: %s", short(code))
		}
	}
	pkgs := append([]string{}, with...)
	switch {
	case from != "":
		pkgs = append(pkgs, from)
	case !hasPackage(pkgs, cmd):
		pkgs = append(pkgs, cmd)
	}
	venv := fmt.Sprintf("%s/venv%d", toolsRoot, n)
	uv := "uv"
	if uvInstalled {
		uv = uvInstallDir + "/uv"
	}
	pyArg := ""
	if python != "" {
		pyArg = " --python " + python
	}
	idxArg := ""
	if len(index) > 0 {
		idxArg = strings.Join(index, " ") + " "
	}
	step := fmt.Sprintf("export UV_PYTHON_INSTALL_DIR=%s UV_CACHE_DIR=/tmp/harbor-uv-cache && %s venv%s %s && %s pip install --python %s/bin/python %s%s && rm -rf /tmp/harbor-uv-cache && chmod -R a+rX %s",
		uvPythonDir, uv, pyArg, venv, uv, venv, idxArg, strings.Join(pkgs, " "), toolsRoot)
	line := prefix + venv + "/bin/" + cmd
	if args := rest[i+1:]; len(args) > 0 {
		line += " " + strings.Join(args, " ")
	}
	return step, line, nil
}

// uvxIndexOptions are the uvx options that choose where packages come from.
// uv pip install takes the same options, so they move to the build step
// unchanged (Terminal-Bench pytorch-model-cli installs torch from the
// PyTorch CPU index).
var uvxIndexOptions = map[string]bool{
	"--index": true, "--default-index": true, "--index-url": true, "-i": true,
	"--extra-index-url": true, "--index-strategy": true, "--find-links": true, "-f": true,
}

// hasPackage reports whether pkgs already holds the distribution name.
func hasPackage(pkgs []string, name string) bool {
	for _, p := range pkgs {
		if strings.EqualFold(packageName(p), name) {
			return true
		}
	}
	return false
}

// packageName is the distribution name of a requirement like pytest==8.4.1.
func packageName(req string) string {
	if i := strings.IndexAny(req, "=<>!~[;@ "); i >= 0 {
		return req[:i]
	}
	return req
}

// solutionFetchRE matches the solution lines that need the network.
var solutionFetchRE = regexp.MustCompile(`(^|[\s;&|(])(` +
	`(apt-get|apt|apk|yum|dnf|conda|mamba|gem|brew)\s+(-\S+\s+)*(install|add)\b|` +
	`pip3?\s+(install|download)\b|uv\s+(pip|add|sync|run|tool|venv|python)\b|uvx\s|` +
	`git\s+(clone|fetch|pull|submodule)\b|` +
	`(npm|pnpm|yarn)\s+(install|ci|i|add)\b|npx\s|` +
	`cargo\s+(install|fetch)\b|go\s+(get|install|mod\s+download)\b|` +
	`(huggingface-cli|hf)\s+download\b)`)

// solutionFetch returns the first line of solve.sh that downloads something,
// or "" when none does. A run has no network, so a reference that needs it
// fails for a reason the agent cannot change; the agent would need the same
// network to solve the task as upstream designed it.
func solutionFetch(src string) string {
	phys := strings.Split(strings.ReplaceAll(src, "\r\n", "\n"), "\n")
	for _, ll := range logicalLines(phys) {
		code := stripComment(ll.text)
		if code == "" {
			continue
		}
		if solutionFetchRE.MatchString(code) {
			return short(code)
		}
		if strings.Contains(code, "curl") || strings.Contains(code, "wget") {
			for _, m := range urlRE.FindAllStringSubmatch(code, -1) {
				if !isLocalHost(m[1]) {
					return short(code)
				}
			}
		}
	}
	return ""
}

// logicalLines joins backslash continuations. Heredoc bodies are kept as one
// line each so their content is never classified as commands.
func logicalLines(phys []string) []logicalLine {
	var out []logicalLine
	for i := 0; i < len(phys); i++ {
		ll := logicalLine{first: i, last: i}
		text := strings.TrimSpace(phys[i])
		for strings.HasSuffix(text, `\`) && ll.last+1 < len(phys) {
			ll.last++
			text = strings.TrimSpace(strings.TrimSuffix(text, `\`)) + " " + strings.TrimSpace(phys[ll.last])
		}
		ll.text = strings.TrimSpace(text)
		if m := heredocRE.FindStringSubmatch(ll.text); m != nil && !strings.HasPrefix(ll.text, "#") {
			for ll.last+1 < len(phys) && strings.TrimSpace(phys[ll.last+1]) != m[1] {
				ll.last++
			}
			if ll.last+1 < len(phys) {
				ll.last++
			}
		}
		out = append(out, ll)
		i = ll.last
	}
	return out
}

// stripComment removes a whole-line comment and a trailing " # comment".
func stripComment(s string) string {
	s = strings.TrimSpace(s)
	if strings.HasPrefix(s, "#") {
		return ""
	}
	if i := strings.Index(s, " #"); i >= 0 && !strings.ContainsAny(s[:i], `"'`) {
		s = strings.TrimSpace(s[:i])
	}
	return s
}

// disabled is the replacement of a line moved to the image build. ":" keeps
// the script valid where the line was the only command of a block.
func disabled(text string) string {
	return ": # " + bakeMarker + ": " + short(text)
}

// withHeader inserts header after the shebang line, or at the top.
func withHeader(script, header string) string {
	if strings.HasPrefix(script, "#!") {
		first, rest, _ := strings.Cut(script, "\n")
		return first + "\n" + header + rest
	}
	return header + script
}

// short keeps one line of at most 160 bytes for messages and comments.
func short(s string) string {
	s = strings.Join(strings.Fields(s), " ")
	if len(s) > 160 {
		return s[:157] + "..."
	}
	return s
}

func isLocalHost(host string) bool {
	h := host
	if i := strings.LastIndexByte(h, ':'); i >= 0 {
		h = h[:i]
	}
	return h == "localhost" || h == "127.0.0.1" || h == "0.0.0.0" || h == "[::1]" || strings.HasPrefix(h, "${") || h == ""
}
