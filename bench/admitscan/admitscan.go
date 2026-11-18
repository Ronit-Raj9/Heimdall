// Package admitscan scans an agent skill folder or an MCP server before it
// is admitted to a run, and writes a pass or block admission record
// (docs/admission-scan.md). The static part reads every file, or every tool,
// prompt and resource listing, for hidden instructions, invisible Unicode,
// encoded payloads, unlisted hosts and commands that fetch and run remote
// code. The dynamic part runs a skill's setup scripts, or the MCP server, in
// a sandboxed container with canary credentials planted in its home folder
// and reports any read of a canary, any network attempt and any canary value
// in the output. The scan complements, and never replaces, running the agent
// with the skill under the bench's probes.
package admitscan

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"os/user"
	"sort"
	"strings"
	"time"

	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/maximhq/bifrost/bench/utils"
)

// Version is the scanner version written into every record. A rule change
// that can change a decision bumps it.
const Version = "1.0.0"

// Defaults for the dynamic run.
const (
	DefaultHelperImage = "bench/coding-core-base:1"
	DefaultWall        = 120 * time.Second
)

// Options configure a scan.
type Options struct {
	AllowHosts  []string // hosts URLs may name; see HostAllowed
	Dynamic     bool     // run setup scripts in the sandbox (always on for MCP scans)
	Image       string   // image the skill's setup scripts run in; default HelperImage
	HelperImage string   // python3 image for the sandbox's planting, watcher and relay; default DefaultHelperImage
	Prefix      string   // docker object name prefix; default "bench-admit-<random>"
	Wall        time.Duration
	Override    string // reason to admit despite block findings; empty means no override
	By          string // who overrides; default the local user name
	Docker      utils.Docker
	Logger      schemas.Logger
	Now         func() time.Time
	Base64Min   int
	HexMin      int
	RugWait     time.Duration // MCP: wait before the last tools/list; default 5 s
}

// Limits are what no admission scan can see. They are written into every
// record and report so a reader never takes "admitted" for "safe".
var Limits = []string{
	"Instruction-only payloads written as ordinary prose that match no pattern pass the static scan.",
	"A server or script that behaves differently for the scanner than for a live agent (by client name, timing, task content or environment) beyond the rug-pull re-listing is not caught.",
	"Time bombs, date checks and payloads that wait longer than the sandbox run are not caught.",
	"Nothing after admission is covered: a later version, a changed server answer or a new tool needs a new scan; the content hash changes with the files.",
	"The dynamic run only sees canary reads, network attempts and canary values in output; it does not judge what a script writes or deletes.",
	"Run the agent with the skill or server under the bench's probes as well: this scan complements that, it does not replace it.",
}

// RuleInfo describes one rule for the docs and the -rules listing.
type RuleInfo struct {
	ID       string `json:"id"`
	Severity string `json:"severity"` // the usual severity; some rules report lower when the evidence is weaker
	What     string `json:"what"`
}

// Rules lists every rule the scanner can report.
func Rules() []RuleInfo {
	b, w, i := schemas.AdmitScanSeverityBlock, schemas.AdmitScanSeverityWarn, schemas.AdmitScanSeverityInfo
	return []RuleInfo{
		{RuleZeroWidth, b, "zero-width or invisible character (U+200B..U+200D, U+2060..U+2064, U+180E, U+FEFF not at file start)"},
		{RuleBidi, b, "bidirectional override or isolate (U+202A..U+202E, U+2066..U+2069)"},
		{RuleBidiMark, w, "direction mark (U+200E, U+200F, U+061C)"},
		{RuleTagChars, b, "Unicode tag characters (U+E0000..U+E007F), decoded payload shown"},
		{RuleHomoglyph, w, "word mixing Latin with Cyrillic or Greek letters"},
		{RuleControl, b, "terminal escape (block) or other control character (warn)"},
		{RuleHTMLComment, b, "HTML comment in markdown holding an injection phrase, or more than three words with an order (do not, always, run, read, send, use, ...); warn for other text, info up to three words"},
		{RuleLinkRefComment, b, "markdown link-reference comment ([//]: # (...)) hiding instructions; same severities as HTML comments"},
		{RuleHiddenCSS, b, "element hidden by CSS (display:none, font-size:0, white text, ...) or the hidden attribute"},
		{RuleAfterBlankLines, b, "text after 20 or more blank lines"},
		{RuleLongWhitespace, b, "text after 80 or more spaces on one line"},
		{RuleInjection, b, "imperative aimed at the model: ignore previous instructions, hide this from the user, you must first run, hidden instruction tags; phrases honest skills also use (do not mention to the user, before doing anything else, silently run) warn unless the line names a URL, download, install, credential or email"},
		{RuleInstallLure, b, "wording that asks the reader to download and run or install something by hand, next to a URL or download (warn without one; plain prerequisite wording is info)"},
		{RuleCredentialPath, w, "names a credential file or store (~/.ssh, .aws/credentials, .netrc, keychain, ...)"},
		{RuleBase64, b, "base64 run over the threshold that decodes to text, a script, an executable or an archive"},
		{RuleHex, b, "hex run or \\x escapes that decode to text, a script, an executable or an archive"},
		{RuleUnlistedHost, b, "URL host not on the allowlist (each host reported once per file)"},
		{RuleReservedHost, i, "URL host reserved for documentation or local use"},
		{RuleRemoteFetch, b, "fetch and run: curl|sh, bash <(curl), eval \"$(curl)\", pip install git+/URL, npm/npx of URLs, iwr|iex"},
		{RuleDecodeExec, b, "base64 -d | sh, PowerShell -EncodedCommand"},
		{RuleDownloadExec, b, "chmod +x within five lines of a download"},
		{RulePipIndex, b, "pip --index-url, --extra-index-url or --find-links to a host not on the allowlist"},
		{RuleInstallHook, w, "package.json script npm runs on install"},
		{RuleInstallHookFetch, b, "install hook running an unpinned package, a download or inline node code"},
		{RuleSkillMDMissing, b, "no SKILL.md regular file at the skill root"},
		{RuleSymlink, b, "symlink in the skill folder (never followed; the adapter refuses them)"},
		{RuleSpecialFile, b, "device, pipe or socket in the skill folder"},
		{RuleExecutable, w, "executable binary (ELF, Mach-O, PE) in the skill folder"},
		{RuleBinary, i, "other binary file, hashed but not scanned"},
		{RuleTooLarge, w, "file over the scan cap: hashed whole, scanned only up to the cap"},
		{RuleScanLimit, b, "more files or bytes than the scan reads; the rest is unscanned"},
		{RuleRugPull, b, "MCP tool listing changed between tools/list calls (after a tool call, a wait, a list_changed or a new session)"},
		{RuleShadowForeign, b, "MCP description names a tool another server or the agent provides"},
		{RuleShadowDirective, b, "MCP description tells the model how to use other tools"},
		{RuleShadowSibling, i, "MCP description names another tool of the same server"},
		{RuleNameCollision, w, "MCP tool named like a common tool of another server or the agent"},
		{RuleMCPListFailed, w, "an MCP listing method failed"},
		{RuleMCPFilesSkipped, w, "MCP service file outside the sandbox home, not placed (the sandbox root is read-only)"},
		{RuleCanaryRead, b, "dynamic: a planted canary credential file was opened or read"},
		{RuleCredentialDirListed, w, "dynamic: a credential folder (~/.ssh, ~/.aws, ~/.config/gh) was opened"},
		{RuleNetworkAttempt, b, "dynamic: a DNS query, TCP connection attempt, UDP datagram or ARP lookup for an outside address"},
		{RuleCanaryInOutput, b, "dynamic: a canary value (or its base64) appeared in the output"},
		{RuleNotObserved, b, "dynamic: the watcher could not run, so the run proves nothing"},
		{RuleSetupTimeout, w, "dynamic: setup ran past the wall budget and was killed"},
		{RuleSetupFailed, i, "dynamic: setup exited non-zero"},
		{RuleNoSetup, i, "dynamic: no setup script or install snippet found to run"},
	}
}

// Decide returns the decision for a set of findings: blocked when any is a
// block finding, unless override holds a reason, then admitted_with_override.
func Decide(findings []schemas.AdmitScanFinding, override string) string {
	for _, f := range findings {
		if f.Severity == schemas.AdmitScanSeverityBlock {
			if strings.TrimSpace(override) != "" {
				return schemas.AdmitScanAdmittedWithOverride
			}
			return schemas.AdmitScanBlocked
		}
	}
	return schemas.AdmitScanAdmitted
}

// finish sorts the findings, counts them, decides and stamps the record.
func finish(rec *schemas.AdmitScanRecord, opts Options) {
	sortFindings(rec.Findings)
	rec.Counts = schemas.AdmitScanCounts{}
	for _, f := range rec.Findings {
		switch f.Severity {
		case schemas.AdmitScanSeverityBlock:
			rec.Counts.Block++
		case schemas.AdmitScanSeverityWarn:
			rec.Counts.Warn++
		default:
			rec.Counts.Info++
		}
	}
	now := opts.now()
	rec.Decision = Decide(rec.Findings, opts.Override)
	if rec.Decision == schemas.AdmitScanAdmittedWithOverride {
		rec.Override = &schemas.AdmitScanOverride{Reason: strings.TrimSpace(opts.Override), By: opts.by(), At: now}
	}
	if rec.ID == "" {
		rec.ID = utils.NewID("adm", 10)
	}
	rec.CreatedAt = now
	rec.ScannerVersion = Version
	rec.AllowHosts = append([]string{}, opts.AllowHosts...)
	sort.Strings(rec.AllowHosts)
	rec.Limits = append([]string(nil), Limits...)
	if rec.Findings == nil {
		rec.Findings = []schemas.AdmitScanFinding{}
	}
}

// sortFindings orders by severity (block first), file, line, column, rule.
func sortFindings(fs []schemas.AdmitScanFinding) {
	rank := map[string]int{schemas.AdmitScanSeverityBlock: 0, schemas.AdmitScanSeverityWarn: 1, schemas.AdmitScanSeverityInfo: 2}
	sort.SliceStable(fs, func(i, j int) bool {
		a, b := fs[i], fs[j]
		if rank[a.Severity] != rank[b.Severity] {
			return rank[a.Severity] < rank[b.Severity]
		}
		if a.File != b.File {
			return a.File < b.File
		}
		if a.Line != b.Line {
			return a.Line < b.Line
		}
		if a.Column != b.Column {
			return a.Column < b.Column
		}
		return a.Rule < b.Rule
	})
}

// ContentHash hashes the files of a scan: one "path NUL sha256" line per
// file in path order, links as "path NUL link:target". Any changed, added or
// removed file changes it.
func ContentHash(files []schemas.AdmitScanFile) string {
	sorted := append([]schemas.AdmitScanFile(nil), files...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].Path < sorted[j].Path })
	h := sha256.New()
	for _, f := range sorted {
		v := f.SHA256
		if f.Symlink != "" {
			v = "link:" + f.Symlink
		}
		fmt.Fprintf(h, "%s\x00%s\n", f.Path, v)
	}
	return "sha256:" + hex.EncodeToString(h.Sum(nil))
}

func (o Options) now() time.Time {
	if o.Now != nil {
		return o.Now().UTC()
	}
	return time.Now().UTC()
}

// by is who overrides: the option, else $USER, else the OS account.
func (o Options) by() string {
	if o.By != "" {
		return o.By
	}
	if u := os.Getenv("USER"); u != "" {
		return u
	}
	if u, err := user.Current(); err == nil {
		return u.Username
	}
	return ""
}

func (o Options) helperImage() string {
	if o.HelperImage != "" {
		return o.HelperImage
	}
	return DefaultHelperImage
}

func (o Options) image() string {
	if o.Image != "" {
		return o.Image
	}
	return o.helperImage()
}

func (o Options) wall() time.Duration {
	if o.Wall > 0 {
		return o.Wall
	}
	return DefaultWall
}

func (o Options) prefix() string {
	if o.Prefix != "" {
		return o.Prefix
	}
	return strings.ReplaceAll(utils.NewID("bench-admit", 8), "_", "-")
}

func (o Options) logger() schemas.Logger {
	if o.Logger != nil {
		return o.Logger
	}
	return utils.NewStdLogger(os.Stderr, false)
}

func (o Options) textOptions(markdown bool) TextOptions {
	return TextOptions{AllowHosts: o.AllowHosts, Markdown: markdown, Base64Min: o.Base64Min, HexMin: o.HexMin}
}
