package admitscan

import (
	"bytes"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"net"
	"net/url"
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/maximhq/bifrost/bench/schemas"
)

// Text rule ids. Every rule of the static scan is listed in Rules.
const (
	RuleZeroWidth        = "unicode-zero-width"
	RuleBidi             = "unicode-bidi"
	RuleBidiMark         = "unicode-bidi-mark"
	RuleTagChars         = "unicode-tag-chars"
	RuleHomoglyph        = "unicode-homoglyph"
	RuleControl          = "unicode-control"
	RuleHTMLComment      = "hidden-html-comment"
	RuleLinkRefComment   = "hidden-link-reference"
	RuleHiddenCSS        = "hidden-css"
	RuleAfterBlankLines  = "hidden-after-blank-lines"
	RuleLongWhitespace   = "hidden-long-whitespace"
	RuleInjection        = "injection-phrase"
	RuleInstallLure      = "prereq-install-lure"
	RuleCredentialPath   = "credential-path-reference"
	RuleBase64           = "encoded-base64"
	RuleHex              = "encoded-hex"
	RuleUnlistedHost     = "url-unlisted-host"
	RuleReservedHost     = "url-reserved-host"
	RuleRemoteFetch      = "remote-code-fetch"
	RuleDecodeExec       = "decode-exec"
	RuleDownloadExec     = "download-exec"
	RulePipIndex         = "pip-unlisted-index"
	RuleInstallHook      = "npm-install-hook"
	RuleInstallHookFetch = "npm-install-hook-unpinned"
)

// Default thresholds of the encoded-blob rules, in characters.
const (
	DefaultBase64Min = 40
	DefaultHexMin    = 64
)

// TextOptions tune ScanText.
type TextOptions struct {
	AllowHosts []string // hosts a URL may name; "*.example.org" or ".example.org" also allows subdomains
	Markdown   bool     // run the markdown hiding rules (HTML comments, link-reference comments, CSS)
	Base64Min  int      // default DefaultBase64Min
	HexMin     int      // default DefaultHexMin
}

// ScanText runs every text rule over content and reports each finding
// against file, with a 1-based line and rune column. It is pure.
func ScanText(file string, content []byte, opts TextOptions) []schemas.AdmitScanFinding {
	if opts.Base64Min <= 0 {
		opts.Base64Min = DefaultBase64Min
	}
	if opts.HexMin <= 0 {
		opts.HexMin = DefaultHexMin
	}
	t := newTextScan(file, content, opts)
	t.unicodeRules()
	if opts.Markdown {
		t.markdownRules()
	}
	t.layoutRules()
	t.phraseRules()
	t.encodedRules()
	t.urlRules()
	t.commandRules()
	return t.out
}

// textScan holds one ScanText call.
type textScan struct {
	file  string
	text  string
	lines []string
	start []int // byte offset of each line
	opts  TextOptions
	out   []schemas.AdmitScanFinding
	seen  map[string]bool
}

func newTextScan(file string, content []byte, opts TextOptions) *textScan {
	text := strings.ToValidUTF8(string(content), "\ufffd")
	t := &textScan{file: file, text: text, opts: opts, seen: map[string]bool{}}
	off := 0
	for _, l := range strings.SplitAfter(text, "\n") {
		if l == "" {
			continue
		}
		t.start = append(t.start, off)
		t.lines = append(t.lines, strings.TrimRight(l, "\r\n"))
		off += len(l)
	}
	return t
}

// pos turns a byte offset of the text into a 1-based line and rune column.
func (t *textScan) pos(off int) (int, int) {
	lo, hi := 0, len(t.start)-1
	for lo < hi {
		mid := (lo + hi + 1) / 2
		if t.start[mid] <= off {
			lo = mid
		} else {
			hi = mid - 1
		}
	}
	if len(t.start) == 0 {
		return 1, 1
	}
	in := off - t.start[lo]
	line := t.lines[lo]
	if in > len(line) {
		in = len(line)
	}
	return lo + 1, utf8.RuneCountInString(line[:in]) + 1
}

// add records a finding at a byte offset. One finding per rule, line and
// detail key keeps a line with fifty zero-width spaces one finding.
func (t *textScan) add(rule, sev string, off int, detail, decoded string) {
	line, col := t.pos(off)
	key := fmt.Sprintf("%s|%d|%s", rule, line, detail)
	if t.seen[key] {
		return
	}
	t.seen[key] = true
	ex := ""
	if line-1 < len(t.lines) {
		ex = Excerpt(t.lines[line-1], col)
	}
	t.out = append(t.out, schemas.AdmitScanFinding{Rule: rule, Severity: sev, File: t.file, Line: line, Column: col, Excerpt: ex, Decoded: decoded, Detail: detail})
}

// unicodeRules finds invisible, direction-changing and tag characters, and
// words that mix Latin with Cyrillic or Greek letters.
func (t *textScan) unicodeRules() {
	for i := 0; i < len(t.text); {
		r, size := utf8.DecodeRuneInString(t.text[i:])
		switch {
		case isZeroWidth(r) && !(r == 0xFEFF && i == 0):
			t.add(RuleZeroWidth, schemas.AdmitScanSeverityBlock, i, fmt.Sprintf("invisible character U+%04X", r), "")
		case (r >= 0x202A && r <= 0x202E) || (r >= 0x2066 && r <= 0x2069):
			t.add(RuleBidi, schemas.AdmitScanSeverityBlock, i, fmt.Sprintf("bidirectional control U+%04X reorders how the text displays", r), "")
		case r == 0x200E || r == 0x200F || r == 0x061C:
			t.add(RuleBidiMark, schemas.AdmitScanSeverityWarn, i, fmt.Sprintf("direction mark U+%04X", r), "")
		case r >= 0xE0000 && r <= 0xE007F:
			end, payload := tagRun(t.text, i)
			t.add(RuleTagChars, schemas.AdmitScanSeverityBlock, i, fmt.Sprintf("%d Unicode tag characters (invisible text a model can read)", utf8.RuneCountInString(t.text[i:end])), payload)
			i = end
			continue
		case r == 0x1B:
			t.add(RuleControl, schemas.AdmitScanSeverityBlock, i, "terminal escape sequence (ESC) can hide or rewrite displayed text", "")
		case r < 0x20 && r != '\t' && r != '\n' && r != '\r', r == 0x7F:
			t.add(RuleControl, schemas.AdmitScanSeverityWarn, i, fmt.Sprintf("control character U+%04X", r), "")
		}
		i += size
	}
	for _, m := range wordRe.FindAllStringIndex(t.text, -1) {
		if w := t.text[m[0]:m[1]]; mixedScript(w) {
			t.add(RuleHomoglyph, schemas.AdmitScanSeverityWarn, m[0], "word mixes Latin with Cyrillic or Greek letters: "+Visible(w), "")
		}
	}
}

// isZeroWidth reports the invisible characters used to hide text.
func isZeroWidth(r rune) bool {
	return (r >= 0x200B && r <= 0x200D) || (r >= 0x2060 && r <= 0x2064) || r == 0xFEFF || r == 0x180E
}

// tagRun returns the end of the run of tag characters starting at i and
// their ASCII reading (U+E0020..U+E007E map to ' '..'~').
func tagRun(s string, i int) (int, string) {
	var b strings.Builder
	for i < len(s) {
		r, size := utf8.DecodeRuneInString(s[i:])
		if r < 0xE0000 || r > 0xE007F {
			break
		}
		if r >= 0xE0020 && r <= 0xE007E {
			b.WriteRune(r - 0xE0000)
		}
		i += size
	}
	return i, b.String()
}

var wordRe = regexp.MustCompile(`[\p{L}]{3,}`)

// mixedScript reports a word holding Latin and Cyrillic or Greek letters,
// the cheap form of a homoglyph check.
func mixedScript(w string) bool {
	var latin, other bool
	for _, r := range w {
		switch {
		case unicode.Is(unicode.Latin, r):
			latin = true
		case unicode.Is(unicode.Cyrillic, r), unicode.Is(unicode.Greek, r):
			other = true
		}
	}
	return latin && other
}

var (
	htmlCommentRe = regexp.MustCompile(`(?s)<!--(.*?)-->`)
	linkRefRe     = regexp.MustCompile(`(?m)^[ \t]{0,3}\[[^\]\n]*\]:[ \t]*(?:#|<>)[ \t]*(?:\(([^)\n]*)\)|"([^"\n]*)"|'([^'\n]*)')`)
	hiddenTagRe   = regexp.MustCompile(`(?is)<([a-z][a-z0-9]*)\b([^>]*)>(.*?)</([a-z][a-z0-9]*)\s*>`)
	hiddenStyleRe = regexp.MustCompile(`(?i)display\s*:\s*none|visibility\s*:\s*hidden|font-size\s*:\s*0(?:\.0+)?(?:px|em|rem|pt|%)?\s*(?:;|"|'|$)|opacity\s*:\s*0(?:\.0+)?\s*(?:;|"|'|$)|color\s*:\s*(?:white|transparent|#fff(?:fff)?\b)|(?:height|width|max-height)\s*:\s*0(?:px)?\s*(?:;|"|'|$)|(?:left|top|text-indent)\s*:\s*-\d{3,}`)
	hiddenAttrRe  = regexp.MustCompile(`(?i)(?:^|\s)(?:hidden|aria-hidden\s*=\s*["']?true)(?:\s|=|$)`)
)

// markdownRules finds text a markdown renderer hides from the reader but a
// model still reads: HTML comments, link-reference comments and elements
// hidden with CSS or the hidden attribute.
func (t *textScan) markdownRules() {
	fences := fencedRanges(t.text)
	for _, m := range htmlCommentRe.FindAllStringSubmatchIndex(t.text, -1) {
		if inRanges(fences, m[0]) {
			continue // a code block shows the comment as text
		}
		body := strings.TrimSpace(t.text[m[2]:m[3]])
		t.hiddenText(RuleHTMLComment, m[0], body, "HTML comment")
	}
	for _, m := range linkRefRe.FindAllStringSubmatchIndex(t.text, -1) {
		if inRanges(fences, m[0]) {
			continue
		}
		body := ""
		for g := 2; g+1 < len(m); g += 2 {
			if m[g] >= 0 {
				body = t.text[m[g]:m[g+1]]
			}
		}
		t.hiddenText(RuleLinkRefComment, m[0], strings.TrimSpace(body), "markdown link-reference comment")
	}
	for _, m := range hiddenTagRe.FindAllStringSubmatchIndex(t.text, -1) {
		if inRanges(fences, m[0]) {
			continue
		}
		attrs, inner := t.text[m[4]:m[5]], strings.TrimSpace(stripTags(t.text[m[6]:m[7]]))
		if inner == "" || !(hiddenStyleRe.MatchString(attrs) || hiddenAttrRe.MatchString(attrs)) {
			continue
		}
		t.add(RuleHiddenCSS, schemas.AdmitScanSeverityBlock, m[0], "element hidden by its style or hidden attribute holds text", clip(inner, 400))
	}
}

// hiddenText reports a hidden span: a block finding when it holds more than
// three words or an injection phrase, info for markers such as "TODO".
func (t *textScan) hiddenText(rule string, off int, body, what string) {
	words := len(strings.Fields(body))
	switch {
	case injectionMatch(body) != "" || (words > 3 && hiddenImperativeRe.MatchString(body)):
		t.add(rule, schemas.AdmitScanSeverityBlock, off, fmt.Sprintf("%s with %d words of instructions is hidden when rendered", what, words), clip(body, 400))
	case words > 3:
		t.add(rule, schemas.AdmitScanSeverityWarn, off, fmt.Sprintf("%s with %d words is hidden when rendered", what, words), clip(body, 400))
	case words > 0:
		t.add(rule, schemas.AdmitScanSeverityInfo, off, fmt.Sprintf("short %s", what), clip(body, 400))
	}
}

// hiddenImperativeRe finds wording that addresses a reader with an order:
// in hidden text the only reader is the model.
var hiddenImperativeRe = regexp.MustCompile(`(?i)\b(?:you\s+(?:must|should|need\s+to|will)|do\s+not|don'?t|never|always|ignore|run|execute|read|send|include|call|install|download|fetch|upload|tell|reply|respond|answer|output|print|copy|paste|open|visit|use)\b`)

var fenceLineRe = regexp.MustCompile("(?m)^[ \t]{0,3}(?:```|~~~)")

// fencedRanges returns the byte ranges of fenced code blocks, where a
// markdown renderer shows comments and tags as text. An unclosed fence runs
// to the end.
func fencedRanges(text string) [][2]int {
	var out [][2]int
	marks := fenceLineRe.FindAllStringIndex(text, -1)
	for i := 0; i < len(marks); i += 2 {
		end := len(text)
		if i+1 < len(marks) {
			end = marks[i+1][1]
		}
		out = append(out, [2]int{marks[i][0], end})
	}
	return out
}

func inRanges(rs [][2]int, off int) bool {
	for _, r := range rs {
		if off >= r[0] && off < r[1] {
			return true
		}
	}
	return false
}

var tagStripRe = regexp.MustCompile(`<[^>]*>`)

func stripTags(s string) string { return tagStripRe.ReplaceAllString(s, " ") }

// Layout thresholds: a run of blank lines that pushes text below the fold,
// and horizontal whitespace that pushes it off the right edge.
const (
	blankRunMin      = 20
	longWhitespace   = 80
	longWhitespaceRe = `[ \t\x{00A0}\x{3000}]{80,}\S`
)

var longWSRe = regexp.MustCompile(longWhitespaceRe)

// layoutRules finds text placed where a reader does not look: after a long
// run of blank lines, or after a long run of spaces on one line.
func (t *textScan) layoutRules() {
	blank := 0
	for i, l := range t.lines {
		if strings.TrimSpace(l) == "" {
			blank++
			continue
		}
		if blank >= blankRunMin {
			t.add(RuleAfterBlankLines, schemas.AdmitScanSeverityBlock, t.start[i], fmt.Sprintf("text after %d blank lines", blank), "")
		}
		blank = 0
		if loc := longWSRe.FindStringIndex(l); loc != nil && !isTableRow(l, loc[1]-1) {
			t.add(RuleLongWhitespace, schemas.AdmitScanSeverityBlock, t.start[i]+loc[1]-1, fmt.Sprintf("text after %d or more spaces on one line", longWhitespace), "")
		}
	}
}

// isTableRow reports a markdown table row, or whitespace that only pads a
// table cell: tables are padded with spaces and render without them.
func isTableRow(line string, next int) bool {
	t := strings.TrimSpace(line)
	return strings.HasPrefix(t, "|") || (next < len(line) && line[next] == '|')
}

// injectionPatterns are imperative phrases aimed at the model rather than
// the user. Each is matched case-insensitively against one line.
var injectionPatterns = []struct {
	name string
	weak bool // common in honest skills too: a warning unless the line also names a URL, a download, an install or a credential
	re   *regexp.Regexp
}{
	{"ignore previous instructions", false, regexp.MustCompile(`(?i)\b(?:ignore|disregard|forget)\s+(?:all\s+|any\s+|the\s+|your\s+)*(?:previous|prior|above|earlier|preceding|system|original|other)\s+(?:instructions?|prompts?|messages?|rules?|guidelines?|directions?)|\boverride\s+(?:all\s+|any\s+|the\s+|your\s+)+(?:previous\s+|prior\s+|system\s+|original\s+)?(?:instructions?|prompts?|rules?|guidelines?)`)},
	{"hide this from the user", false, regexp.MustCompile(`(?i)\b(?:do\s+not|don'?t|never|must\s+not)\s+(?:tell|inform|mention|reveal|show|disclose)\s+(?:(?:this|these|it)\s+(?:step|steps|instructions?)?\s*to\s+(?:the\s+)?user|(?:the\s+)?user\s+(?:about\s+)?(?:this|these|that\s+you|what\s+you|the\s+(?:step|instructions?|file|command|request)))|\b(?:keep|hide)\s+(?:this|these|it)\b[^.\n]{0,20}\bfrom\s+(?:the\s+)?user|\buser\b[^.\n]{0,20}\b(?:must|should)\s+not\s+(?:know|see|find\s+out|notice)`)},
	{"without telling the user", false, regexp.MustCompile(`(?i)\bwithout\s+(?:telling|informing|asking|notifying|alerting)\s+(?:the\s+)?(?:user|human|operator)`)},
	{"do not tell the user", true, regexp.MustCompile(`(?i)\b(?:do\s+not|don'?t|never|must\s+not)\s+(?:tell|inform|mention|reveal|show|disclose|notify|alert)\b[^.\n]{0,40}\b(?:user|human|operator|anyone)`)},
	{"before doing anything else", true, regexp.MustCompile(`(?i)\bbefore\s+(?:doing|you\s+do|you\s+(?:answer|respond|reply)|answering|responding|replying)\b[^.\n]{0,30}\b(?:anything\s+else|run|execute|call|install|read|fetch|send|upload)`)},
	{"you must first run", false, regexp.MustCompile(`(?i)\b(?:you\s+must|always|first)\s+(?:first\s+)?(?:run|execute|install)\b[^\n]{0,60}\bbefore\s+(?:anything|any\s+other|answering|responding|replying|using\s+(?:this|the)\s+skill|you\s+(?:do|answer|respond|reply|start))`)},
	{"hidden instruction tag", false, regexp.MustCompile(`(?i)<\s*/?\s*(?:important|hidden|system-reminder|secret[-_ ]instructions?)\s*>`)},
	{"new instructions", false, regexp.MustCompile(`(?i)\b(?:new|updated|real|true)\s+(?:system\s+)?instructions?\s*:`)},
	{"exfiltrate", true, regexp.MustCompile(`(?i)\b(?:send|upload|post|forward|copy|bcc)\b[^.\n]{0,30}\b(?:the|your|all|any|these|those|their|its|my|our)\s+(?:[\w-]+\s+)?(?:(?:credentials?|secrets?|tokens?|private\s+keys?|api\s+keys?|passwords?|id_rsa|ssh\s+keys?)\b|\.env\b)[^.\n]{0,40}\b(?:to|into)\b`)},
	{"conceal the action", true, regexp.MustCompile(`(?i)\b(?:silently|secretly|quietly|covertly)\s+(?:run|execute|send|upload|install|download|copy|read)`)},
}

// injectionMatch returns the name of the first injection pattern s
// matches, or "".
func injectionMatch(s string) string {
	name, _, _ := injectionMatchAt(s)
	return name
}

// injectionMatchAt returns the first injection pattern s matches outside
// quotes, whether it is a weak one, and its byte offset. A phrase opened by
// a quote or backtick is quoted as an example (security guidance does
// this), not addressed to the model, and is skipped.
func injectionMatchAt(s string) (string, bool, int) {
	for _, p := range injectionPatterns {
		for _, loc := range p.re.FindAllStringIndex(s, -1) {
			if loc[0] > 0 && strings.ContainsAny(s[loc[0]-1:loc[0]], "\"'`") {
				continue
			}
			if r, _ := utf8.DecodeLastRuneInString(s[:loc[0]]); r == '\u201c' || r == '\u2018' {
				continue
			}
			return p.name, p.weak, loc[0]
		}
	}
	return "", false, -1
}

var emailRe = regexp.MustCompile(`[\w.+-]+@[\w-]+\.[\w.-]+`)

// riskyContext reports a line that names a URL, a download, an install, a
// credential or an email address: what turns a weak injection phrase into
// a block finding.
func riskyContext(line string) bool {
	return urlNearRe.MatchString(line) || credPathRe.MatchString(line) || emailRe.MatchString(line) || installWordRe.MatchString(line)
}

var installWordRe = regexp.MustCompile(`(?i)\b(?:install|download|exec|eval)\b`)

var (
	// lureRe is the strong form of the ClawHub lure: wording that asks the
	// reader to fetch and run something by hand. weakLureRe is ordinary
	// prerequisite wording, a warning at most.
	lureRe     = regexp.MustCompile(`(?i)\b(?:download\s+and\s+(?:run|install|execute|open|launch)|paste\s+(?:this|the\s+following|it)\b[^.\n]{0,30}\b(?:terminal|shell|console)|run\s+the\s+following\s+(?:command|script|one-liner)\s+to\s+install|(?:install|download)\s+the\s+(?:helper|agent|runtime|driver|plugin|extension|binary|installer)\s+from|before\s+using\s+this\s+skill\b[^.\n]{0,40}\b(?:install|download|run)|requires?\s+you\s+to\s+(?:install|download|run)|password[^.\n]{0,20}\b(?:zip|archive|7z|rar)|(?:zip|archive|7z|rar)\b[^.\n]{0,20}\bpassword)`)
	weakLureRe = regexp.MustCompile(`(?i)\b(?:prerequisites?|pre-requisites?|required\s+(?:setup|dependency|component|helper|tool|step)|must\s+(?:be\s+)?installed?)\b`)
	credPathRe = regexp.MustCompile(`(?i)~/\.ssh\b|\.ssh/id_|\bid_(?:rsa|ed25519|ecdsa|dsa)\b|\.aws/credentials|\.netrc\b|\.config/gh/hosts\.ya?ml|\.docker/config\.json|\.kube/config|\.gnupg\b|\bkeychain\b|login\.keychain|wallet\.dat|Login\s+Data`)
	urlNearRe  = regexp.MustCompile(`(?i)\b(?:https?|ftp)://|\b(?:curl|wget|iwr|invoke-webrequest)\b`)
)

// phraseRules finds injection phrases, fake prerequisite-install lures and
// references to credential files.
func (t *textScan) phraseRules() {
	for i, l := range t.lines {
		if name, weak, at := injectionMatchAt(l); name != "" {
			sev := schemas.AdmitScanSeverityBlock
			if weak && !riskyContext(l) {
				sev = schemas.AdmitScanSeverityWarn
			}
			t.add(RuleInjection, sev, t.start[i]+at, "imperative aimed at the model: "+name, "")
		}
		if loc := lureRe.FindStringIndex(l); loc != nil {
			sev, detail := schemas.AdmitScanSeverityWarn, "asks the reader to fetch or install something by hand"
			if t.nearFetch(i, 3) {
				sev, detail = schemas.AdmitScanSeverityBlock, "asks the reader to fetch or install something by hand, next to a download or URL (the ClawHub lure)"
			}
			t.add(RuleInstallLure, sev, t.start[i]+loc[0], detail, "")
		} else if loc := weakLureRe.FindStringIndex(l); loc != nil {
			t.add(RuleInstallLure, schemas.AdmitScanSeverityInfo, t.start[i]+loc[0], "prerequisite wording", "")
		}
		if loc := credPathRe.FindStringIndex(l); loc != nil {
			t.add(RuleCredentialPath, schemas.AdmitScanSeverityWarn, t.start[i]+loc[0], "names a credential file or store: "+l[loc[0]:loc[1]], "")
		}
	}
}

// nearFetch reports a URL or download command within n lines of line i.
func (t *textScan) nearFetch(i, n int) bool {
	for j := max(0, i-n); j < len(t.lines) && j <= i+n; j++ {
		if urlNearRe.MatchString(t.lines[j]) {
			return true
		}
	}
	return false
}

var (
	b64Re     = regexp.MustCompile(`[A-Za-z0-9+/_-]{20,}={0,2}`)
	hexRe     = regexp.MustCompile(`\b[0-9a-fA-F]{32,}\b`)
	hexEscRe  = regexp.MustCompile(`(?:\\x[0-9a-fA-F]{2}){16,}`)
	dataURIRe = regexp.MustCompile(`(?i)data:image/(?:png|jpe?g|gif|webp|x-icon|vnd\.microsoft\.icon|avif|bmp);base64,$`)
)

// encodedRules decodes long base64 and hex runs and reports the ones that
// decode to readable text or to an executable or archive, with a preview.
// Runs that decode to other bytes (hashes, images, keys) are not reported.
func (t *textScan) encodedRules() {
	for _, m := range b64Re.FindAllStringIndex(t.text, -1) {
		s := t.text[m[0]:m[1]]
		if len(s) < t.opts.Base64Min || dataURIRe.MatchString(t.text[max(0, m[0]-40):m[0]]) || !looksEncoded(s) {
			continue
		}
		if dec, ok := decodeBase64(s); ok {
			t.decoded(RuleBase64, m[0], "base64", dec)
		}
	}
	for _, m := range hexRe.FindAllStringIndex(t.text, -1) {
		s := t.text[m[0]:m[1]]
		if len(s) < t.opts.HexMin || len(s)%2 != 0 {
			continue
		}
		if dec, err := hex.DecodeString(s); err == nil {
			t.decoded(RuleHex, m[0], "hex", dec)
		}
	}
	for _, m := range hexEscRe.FindAllStringIndex(t.text, -1) {
		s := strings.ReplaceAll(t.text[m[0]:m[1]], `\x`, "")
		if dec, err := hex.DecodeString(s); err == nil {
			t.decoded(RuleHex, m[0], `\x escapes`, dec)
		}
	}
}

// decoded reports one decoded blob when it is text or a known executable.
func (t *textScan) decoded(rule string, off int, enc string, dec []byte) {
	if kind := executableKind(dec); kind != "" {
		t.add(rule, schemas.AdmitScanSeverityBlock, off, fmt.Sprintf("%s blob decodes to %s (%d bytes)", enc, kind, len(dec)), Visible(clip(string(bytes.ToValidUTF8(dec[:min(len(dec), 64)], []byte("?"))), 120)))
		return
	}
	if isReadableText(dec) {
		t.add(rule, schemas.AdmitScanSeverityBlock, off, fmt.Sprintf("%s blob decodes to text (%d bytes)", enc, len(dec)), Visible(clip(string(dec), 400)))
	}
}

// looksEncoded skips runs that are plainly words or paths: base64 of real
// data mixes cases and digits.
func looksEncoded(s string) bool {
	var upper, lower, digit bool
	for _, r := range s {
		switch {
		case r >= 'A' && r <= 'Z':
			upper = true
		case r >= 'a' && r <= 'z':
			lower = true
		case r >= '0' && r <= '9':
			digit = true
		}
	}
	return upper && lower && (digit || strings.ContainsAny(s, "+/="))
}

// decodeBase64 tries the standard and URL alphabets, padded and raw.
func decodeBase64(s string) ([]byte, bool) {
	trimmed := strings.TrimRight(s, "=")
	for _, enc := range []*base64.Encoding{base64.RawStdEncoding, base64.RawURLEncoding} {
		if b, err := enc.DecodeString(trimmed); err == nil {
			return b, true
		}
	}
	// A run may carry a stray character at either end; drop to a multiple of 4.
	if n := len(trimmed) / 4 * 4; n >= 16 {
		if b, err := base64.RawStdEncoding.DecodeString(trimmed[:n]); err == nil {
			return b, true
		}
	}
	return nil, false
}

// executableKind names an executable, script or archive by its first bytes.
func executableKind(b []byte) string {
	switch {
	case bytes.HasPrefix(b, []byte("\x7fELF")):
		return "an ELF executable"
	case bytes.HasPrefix(b, []byte("MZ")) && len(b) > 64:
		return "a Windows executable"
	case bytes.HasPrefix(b, []byte{0xcf, 0xfa, 0xed, 0xfe}), bytes.HasPrefix(b, []byte{0xce, 0xfa, 0xed, 0xfe}), bytes.HasPrefix(b, []byte{0xca, 0xfe, 0xba, 0xbe}):
		return "a Mach-O executable"
	case bytes.HasPrefix(b, []byte("#!")):
		return "a script with a #! line"
	case bytes.HasPrefix(b, []byte("PK\x03\x04")):
		return "a zip archive"
	case bytes.HasPrefix(b, []byte{0x1f, 0x8b}):
		return "gzip data"
	case bytes.HasPrefix(b, []byte("7z\xbc\xaf\x27\x1c")):
		return "a 7z archive"
	}
	return ""
}

// isReadableText reports valid UTF-8 that is mostly printable and holds
// some letters: what a hidden instruction or script looks like decoded.
func isReadableText(b []byte) bool {
	if len(b) < 12 || !utf8.Valid(b) {
		return false
	}
	var printable, letters, total int
	for _, r := range string(b) {
		total++
		if unicode.IsPrint(r) || r == '\n' || r == '\t' || r == '\r' {
			printable++
		}
		if unicode.IsLetter(r) {
			letters++
		}
	}
	return printable*100 >= total*90 && letters >= 8
}

var urlRe = regexp.MustCompile(`(?i)\b(?:https?|ftp|wss?|git|ssh|sftp)://[^\s"'<>()\[\]{}` + "`" + `\\|]+`)

// urlRules reports every host a URL names that is not on the allowlist,
// once per host and file.
func (t *textScan) urlRules() {
	reported := map[string]bool{}
	for _, m := range urlRe.FindAllStringIndex(t.text, -1) {
		host := urlHost(t.text[m[0]:m[1]])
		if host == "" || reported[host] {
			continue
		}
		reported[host] = true
		switch {
		case HostAllowed(host, t.opts.AllowHosts):
			continue
		case reservedHost(host):
			t.add(RuleReservedHost, schemas.AdmitScanSeverityInfo, m[0], "URL names reserved or local host "+host, "")
		case net.ParseIP(host) != nil:
			t.add(RuleUnlistedHost, schemas.AdmitScanSeverityBlock, m[0], "URL names IP address "+host+", not on the allowlist", "")
		default:
			t.add(RuleUnlistedHost, schemas.AdmitScanSeverityBlock, m[0], "URL names host "+host+", not on the allowlist", "")
		}
	}
}

// urlHost returns the lowercased host of a URL, or "".
func urlHost(raw string) string {
	raw = strings.TrimRight(raw, ".,;:!?*_~")
	u, err := url.Parse(raw)
	if err != nil {
		return ""
	}
	return strings.TrimSuffix(strings.ToLower(u.Hostname()), ".")
}

// HostAllowed reports whether host is on the allowlist. An entry
// "*.example.org" or ".example.org" allows every subdomain and the domain
// itself; any other entry matches exactly.
func HostAllowed(host string, allow []string) bool {
	host = strings.ToLower(strings.TrimSuffix(host, "."))
	for _, a := range allow {
		a = strings.ToLower(strings.TrimSpace(a))
		base := strings.TrimPrefix(strings.TrimPrefix(a, "*"), ".")
		switch {
		case a == "":
		case host == a:
			return true
		case strings.HasPrefix(a, "*.") || strings.HasPrefix(a, "."):
			if host == base || strings.HasSuffix(host, "."+base) {
				return true
			}
		}
	}
	return false
}

// reservedHost reports the names RFC 2606 and RFC 6761 keep for
// documentation and local use, which no one can register.
func reservedHost(h string) bool {
	switch h {
	case "localhost", "example.com", "example.net", "example.org", "127.0.0.1", "::1", "0.0.0.0":
		return true
	}
	for _, s := range []string{".example", ".test", ".invalid", ".localhost", ".example.com", ".example.net", ".example.org"} {
		if strings.HasSuffix(h, s) {
			return true
		}
	}
	return false
}

// Visible makes invisible and control characters readable as <U+XXXX> so an
// excerpt shows what a renderer hides.
func Visible(s string) string {
	var b strings.Builder
	for _, r := range s {
		switch {
		case r == '\t':
			b.WriteString("\\t")
		case isZeroWidth(r), (r >= 0x202A && r <= 0x202E), (r >= 0x2066 && r <= 0x2069), r == 0x200E, r == 0x200F, r == 0x061C, r == 0x00AD,
			(r >= 0xE0000 && r <= 0xE007F), r < 0x20, r == 0x7F, (r >= 0x80 && r < 0xA0):
			fmt.Fprintf(&b, "<U+%04X>", r)
		default:
			b.WriteRune(r)
		}
	}
	return b.String()
}

// Excerpt returns up to about 160 runes of line around the 1-based rune
// column, with invisible characters made visible.
func Excerpt(line string, col int) string {
	rs := []rune(line)
	from := max(0, col-1-60)
	to := min(len(rs), from+160)
	s := Visible(string(rs[from:to]))
	if from > 0 {
		s = "..." + s
	}
	if to < len(rs) {
		s += "..."
	}
	return s
}

// clip shortens s to n runes with a trailing "...".
func clip(s string, n int) string {
	rs := []rune(s)
	if len(rs) <= n {
		return s
	}
	return string(rs[:n]) + "..."
}
