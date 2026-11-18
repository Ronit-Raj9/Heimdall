package admitscan

import (
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strings"

	"github.com/maximhq/bifrost/bench/schemas"
)

// fetchPatterns are shell, Python and PowerShell forms that fetch code from
// the network and run it in one step. They are matched against one logical
// line (backslash continuations joined).
var fetchPatterns = []struct {
	rule, what string
	re         *regexp.Regexp
}{
	{RuleRemoteFetch, "download piped to an interpreter", regexp.MustCompile(`(?i)\b(?:curl|wget|fetch)\b[^|\n]*\|\s*(?:sudo\s+(?:-\S+\s+)*)?(?:env\s+)?(?:ba|z|da|k|fi|c|tc)?sh\b|\b(?:curl|wget)\b[^|\n]*\|\s*(?:sudo\s+(?:-\S+\s+)*)?(?:python[0-9.]*|perl|ruby|node|php|bun|deno)\b`)},
	{RuleRemoteFetch, "process substitution of a download", regexp.MustCompile(`(?i)(?:\b(?:ba|z)?sh|\bsource|(?:^|[;&|]\s*)\.)\s+<\(\s*(?:curl|wget)\b`)},
	{RuleRemoteFetch, "eval of a download", regexp.MustCompile("(?i)\\beval\\s+[\"']?(?:\\$\\(|`)\\s*(?:curl|wget)\\b")},
	{RuleRemoteFetch, "interpreter -c of a download", regexp.MustCompile("(?i)\\b(?:python[0-9.]*|perl|ruby|node|(?:ba|z)?sh)\\s+-[ce]\\s+[\"']?(?:\\$\\(|`)\\s*(?:curl|wget)\\b")},
	{RuleRemoteFetch, "download then run in one line", regexp.MustCompile(`(?i)\b(?:curl|wget)\b[^\n]*(?:&&|;)\s*(?:sudo\s+)?(?:(?:(?:ba|z)?sh|python[0-9.]*)\s+\S|\.?/[\w./-]+\s*(?:$|[;&|]))`)},
	{RuleRemoteFetch, "npm or npx of a URL or git", regexp.MustCompile(`(?i)\b(?:npm|pnpm|yarn|bun)\s+(?:install|i|add)\b[^\n]*\s(?:git\+|https?://|github:|gitlab:|bitbucket:)|\b(?:npx|bunx|pnpm\s+dlx)\s+(?:-\S+\s+)*(?:git\+|https?://|github:)`)},
	{RuleRemoteFetch, "PowerShell download and invoke", regexp.MustCompile(`(?i)\b(?:iwr|irm|invoke-webrequest|invoke-restmethod|downloadstring|downloadfile|net\.webclient|start-bitstransfer)\b[^\n]*\b(?:iex|invoke-expression)\b|\b(?:iex|invoke-expression)\b[^\n]*\b(?:iwr|irm|invoke-webrequest|downloadstring|net\.webclient)\b`)},
	{RuleDecodeExec, "PowerShell encoded command", regexp.MustCompile(`(?i)\b(?:powershell|pwsh)(?:\.exe)?\b[^\n]*\s-e(?:nc|ncodedcommand)?\s+[A-Za-z0-9+/=]{20,}`)},
	{RuleDecodeExec, "base64 decoded into an interpreter", regexp.MustCompile(`(?i)\bbase64\s+(?:-d|--decode|-D)\b[^\n]*\|\s*(?:sudo\s+)?(?:(?:ba|z|da)?sh|python[0-9.]*|perl|ruby|node)\b|\bopenssl\s+(?:base64|enc)\b[^\n]*-d\b[^\n]*\|\s*(?:(?:ba|z)?sh|python[0-9.]*)\b`)},
}

var (
	downloadToFileRe = regexp.MustCompile(`(?i)\b(?:curl\b[^\n]*\s(?:-o|-O|--output|--remote-name)\b|wget\b|invoke-webrequest\b[^\n]*-outfile)`)
	chmodExecRe      = regexp.MustCompile(`\bchmod\s+(?:-\S+\s+)*(?:[ugoa]*\+[rw]*x|[0-7]?[0-7]*[1357][0-7]*)\b`)
	pipIndexRe       = regexp.MustCompile(`(?i)\b(?:pip[0-9.]*|uv\s+pip)\s+install\b[^\n]*?(?:--(?:extra-)?index-url|-i|--find-links|-f)[\s=]+(\S+)`)
)

// downloadExecWindow is how many lines after a download a chmod +x still
// counts as making the download executable.
const downloadExecWindow = 5

// commandRules finds commands that fetch remote code and run it.
func (t *textScan) commandRules() {
	lastDownload := -downloadExecWindow - 1
	for i := 0; i < len(t.lines); i++ {
		first := i
		logical := t.lines[i]
		for strings.HasSuffix(logical, `\`) && i+1 < len(t.lines) {
			i++
			logical = strings.TrimSuffix(logical, `\`) + " " + strings.TrimSpace(t.lines[i])
		}
		off := t.start[first]
		for _, p := range fetchPatterns {
			if p.re.MatchString(logical) {
				t.add(p.rule, schemas.AdmitScanSeverityBlock, off, p.what, "")
			}
		}
		if pipFromURL(logical) {
			t.add(RuleRemoteFetch, schemas.AdmitScanSeverityBlock, off, "pip install from a URL or git", "")
		}
		for _, m := range pipIndexRe.FindAllStringSubmatch(logical, -1) {
			if host := urlHost(m[1]); host != "" && !HostAllowed(host, t.opts.AllowHosts) {
				t.add(RulePipIndex, schemas.AdmitScanSeverityBlock, off, "pip installs from index host "+host+", not on the allowlist", "")
			}
		}
		if downloadToFileRe.MatchString(logical) {
			lastDownload = first
		}
		if chmodExecRe.MatchString(logical) && first-lastDownload <= downloadExecWindow {
			t.add(RuleDownloadExec, schemas.AdmitScanSeverityBlock, off, fmt.Sprintf("chmod +x within %d lines of a download", downloadExecWindow), "")
		}
	}
}

var pipInstallRe = regexp.MustCompile(`(?i)\b(?:pip[0-9.]*|uv\s+pip|pipx)\s+install\b(.*)`)

// pipFromURL reports a pip install whose package argument is a URL or a VCS
// reference. The values of index and find-links flags are not packages and
// are checked by the pip-unlisted-index rule instead.
func pipFromURL(line string) bool {
	m := pipInstallRe.FindStringSubmatch(line)
	if m == nil {
		return false
	}
	fields := strings.Fields(m[1])
	for i := 0; i < len(fields); i++ {
		f := strings.ToLower(strings.Trim(fields[i], `"'`))
		switch {
		case f == "-i" || f == "-f" || f == "--index-url" || f == "--extra-index-url" || f == "--find-links" || f == "--trusted-host" || f == "-c" || f == "-r":
			i++
		case strings.HasPrefix(f, "-"):
		case strings.HasPrefix(f, "git+") || strings.HasPrefix(f, "http://") || strings.HasPrefix(f, "https://") || strings.HasPrefix(f, "svn+") || strings.HasPrefix(f, "hg+") || strings.Contains(f, "@ http") || strings.Contains(f, "@git+"):
			return true
		case f == "&&" || f == ";" || f == "|":
			return false
		}
	}
	return false
}

// npmHooks are the package.json scripts npm runs on install.
var npmHooks = []string{"preinstall", "install", "postinstall", "prepare", "prepublish"}

var (
	npxUnpinnedRe = regexp.MustCompile(`(?i)\b(?:npx|bunx|pnpm\s+dlx)\s+(?:-\S+\s+)*((?:@[a-z0-9._-]+/)?[a-z0-9._-]+)(@\S+)?`)
	npmAddRe      = regexp.MustCompile(`(?i)\b(?:npm|pnpm|yarn|bun)\s+(?:install|i|add)\s+((?:-\S+\s+)*)((?:@[a-z0-9._-]+/)?[a-z0-9._-]+)(@\S+)?`)
	nodeEvalRe    = regexp.MustCompile(`(?i)\bnode\s+-e\b|\b(?:curl|wget)\b`)
)

// ScanPackageJSON reports npm install hooks in a package.json: every hook is
// a warning (it runs on `npm install`), and a hook that runs an unpinned
// package, a download or inline node code is a block finding. It is pure; a
// file that is not JSON yields nothing.
func ScanPackageJSON(file string, content []byte) []schemas.AdmitScanFinding {
	var pkg struct {
		Scripts map[string]string `json:"scripts"`
	}
	if json.Unmarshal(content, &pkg) != nil {
		return nil
	}
	t := newTextScan(file, content, TextOptions{})
	hooks := make([]string, 0, len(npmHooks))
	for _, h := range npmHooks {
		if _, ok := pkg.Scripts[h]; ok {
			hooks = append(hooks, h)
		}
	}
	sort.Strings(hooks)
	for _, h := range hooks {
		cmd := pkg.Scripts[h]
		off := strings.Index(t.text, `"`+h+`"`)
		if off < 0 {
			off = 0
		}
		t.add(RuleInstallHook, schemas.AdmitScanSeverityWarn, off, fmt.Sprintf("npm runs the %q script on install: %s", h, clip(cmd, 120)), "")
		if why := unpinnedHook(cmd); why != "" {
			t.add(RuleInstallHookFetch, schemas.AdmitScanSeverityBlock, off, fmt.Sprintf("install hook %q %s", h, why), "")
		}
	}
	return t.out
}

// unpinnedHook says why an install hook command fetches or runs code that
// no lock pins, or returns "".
func unpinnedHook(cmd string) string {
	for _, m := range npxUnpinnedRe.FindAllStringSubmatch(cmd, -1) {
		if m[2] == "" || m[2] == "@latest" {
			return "runs npx package " + m[1] + " without a pinned version"
		}
	}
	for _, m := range npmAddRe.FindAllStringSubmatch(cmd, -1) {
		if m[3] == "" || m[3] == "@latest" {
			return "installs package " + m[2] + " without a pinned version"
		}
	}
	if nodeEvalRe.MatchString(cmd) {
		return "downloads or evaluates code inline"
	}
	return ""
}
