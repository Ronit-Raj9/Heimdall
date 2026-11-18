package admitscan

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/maximhq/bifrost/bench/schemas"
)

// Skill and dynamic rule ids (the text rules are in text.go, the MCP rules
// in mcp.go).
const (
	RuleSkillMDMissing      = "skill-md-missing"
	RuleSymlink             = "symlink"
	RuleSpecialFile         = "special-file"
	RuleExecutable          = "binary-executable"
	RuleBinary              = "binary-file"
	RuleTooLarge            = "file-too-large"
	RuleScanLimit           = "scan-limit"
	RuleCanaryRead          = "dyn-canary-read"
	RuleCredentialDirListed = "dyn-credential-dir-listed"
	RuleNetworkAttempt      = "dyn-network-attempt"
	RuleCanaryInOutput      = "dyn-canary-in-output"
	RuleNotObserved         = "dyn-not-observed"
	RuleSetupTimeout        = "dyn-setup-timeout"
	RuleSetupFailed         = "dyn-setup-failed"
	RuleNoSetup             = "dyn-no-setup"
)

// Scan bounds for a skill folder.
const (
	MaxFileScanBytes  = 4 << 20  // bytes of one file the text rules read
	MaxTotalScanBytes = 64 << 20 // bytes of the whole folder the text rules read
	MaxFiles          = 5000
	maxPlantBytes     = 32 << 20 // skill bytes copied into the sandbox
)

// SkillFile is the name every skill folder must hold.
const SkillFile = "SKILL.md"

// skillFile is one walked entry with what the scan read of it.
type skillFile struct {
	meta schemas.AdmitScanFile
	mode fs.FileMode
	data []byte // the scanned prefix of a regular file
}

// ScanSkill scans one skill folder and returns its admission record. The
// folder itself may not be a symlink; links inside it are reported and never
// followed. With opts.Dynamic the setup scripts run in the sandbox. The
// error is for a folder that cannot be read or a sandbox that cannot start,
// never for a finding.
func ScanSkill(ctx context.Context, dir string, opts Options) (*schemas.AdmitScanRecord, error) {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return nil, fmt.Errorf("resolve skill folder %s: %w", dir, err)
	}
	st, err := os.Lstat(abs)
	if err != nil {
		return nil, fmt.Errorf("stat skill folder: %w", err)
	}
	if !st.IsDir() {
		return nil, fmt.Errorf("skill folder %s is not a folder (symlinks are not followed)", abs)
	}
	files, findings, err := walkSkill(abs)
	if err != nil {
		return nil, err
	}
	for _, f := range files {
		findings = append(findings, scanSkillFile(f, opts)...)
	}
	rec := &schemas.AdmitScanRecord{Kind: schemas.AdmitScanKindSkill, Subject: filepath.Base(abs), Source: abs}
	for _, f := range files {
		rec.Files = append(rec.Files, f.meta)
	}
	rec.ContentHash = ContentHash(rec.Files)
	if opts.Dynamic {
		dyn, dynFindings, err := runSkillDynamic(ctx, rec.Subject, files, opts)
		if err != nil {
			return nil, err
		}
		rec.Dynamic = dyn
		findings = append(findings, dynFindings...)
	}
	rec.Findings = findings
	finish(rec, opts)
	return rec, nil
}

// walkSkill lists every entry under root without following links, hashes
// regular files whole and keeps the scanned prefix of each.
func walkSkill(root string) ([]skillFile, []schemas.AdmitScanFinding, error) {
	var files []skillFile
	var findings []schemas.AdmitScanFinding
	total, limited := int64(0), false
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return fmt.Errorf("walk %s: %w", p, err)
		}
		if p == root || d.IsDir() {
			return nil
		}
		rel, err := filepath.Rel(root, p)
		if err != nil {
			return fmt.Errorf("relative path of %s: %w", p, err)
		}
		rel = filepath.ToSlash(rel)
		if len(files) >= MaxFiles {
			limited = true
			return filepath.SkipAll
		}
		f, err := readSkillEntry(p, rel, d, &total)
		if err != nil {
			return err
		}
		files = append(files, f)
		return nil
	})
	if err != nil {
		return nil, nil, fmt.Errorf("scan skill folder %s: %w", root, err)
	}
	if limited {
		findings = append(findings, schemas.AdmitScanFinding{Rule: RuleScanLimit, Severity: schemas.AdmitScanSeverityBlock, File: ".", Detail: fmt.Sprintf("more than %d files; the rest were not scanned or hashed", MaxFiles)})
	}
	if total > MaxTotalScanBytes {
		findings = append(findings, schemas.AdmitScanFinding{Rule: RuleScanLimit, Severity: schemas.AdmitScanSeverityBlock, File: ".", Detail: fmt.Sprintf("more than %d bytes of text; files past the cap were hashed but not scanned", MaxTotalScanBytes)})
	}
	if !hasSkillMD(files) {
		findings = append(findings, schemas.AdmitScanFinding{Rule: RuleSkillMDMissing, Severity: schemas.AdmitScanSeverityBlock, File: SkillFile, Detail: "the folder has no SKILL.md regular file at its root; the bench refuses it as a skill"})
	}
	return files, findings, nil
}

// readSkillEntry stats, hashes and reads the scanned prefix of one entry.
func readSkillEntry(p, rel string, d fs.DirEntry, total *int64) (skillFile, error) {
	info, err := d.Info()
	if err != nil {
		return skillFile{}, fmt.Errorf("stat %s: %w", rel, err)
	}
	f := skillFile{meta: schemas.AdmitScanFile{Path: rel, Size: info.Size()}, mode: info.Mode()}
	switch {
	case info.Mode()&fs.ModeSymlink != 0:
		target, err := os.Readlink(p)
		if err != nil {
			return f, fmt.Errorf("read link %s: %w", rel, err)
		}
		f.meta.Symlink = target
		f.meta.Note = "symlink, not followed"
		return f, nil
	case !info.Mode().IsRegular():
		f.meta.Note = "special file: " + info.Mode().Type().String()
		return f, nil
	}
	fh, err := os.Open(p)
	if err != nil {
		return f, fmt.Errorf("open %s: %w", rel, err)
	}
	defer fh.Close()
	// The entry was stat'ed without following links; refuse a file swapped
	// for a link (or anything else) between that stat and the open.
	if opened, err := fh.Stat(); err != nil || !os.SameFile(opened, info) {
		return f, fmt.Errorf("%s changed while it was scanned", rel)
	}
	h := sha256.New()
	budget := int64(MaxFileScanBytes)
	if rest := MaxTotalScanBytes - *total; rest < budget {
		budget = max(rest, 0)
	}
	var head bytes.Buffer
	if _, err := io.Copy(io.MultiWriter(h, &limitWriter{w: &head, n: budget}), fh); err != nil {
		return f, fmt.Errorf("read %s: %w", rel, err)
	}
	f.meta.SHA256 = hex.EncodeToString(h.Sum(nil))
	f.data = head.Bytes()
	*total += int64(len(f.data))
	if info.Size() > int64(len(f.data)) {
		*total += info.Size() - int64(len(f.data))
	}
	return f, nil
}

// limitWriter keeps the first n bytes written and discards the rest
// without error, so the hash still covers the whole file.
type limitWriter struct {
	w io.Writer
	n int64
}

func (l *limitWriter) Write(p []byte) (int, error) {
	if l.n > 0 {
		k := min(int64(len(p)), l.n)
		if _, err := l.w.Write(p[:k]); err != nil {
			return 0, err
		}
		l.n -= k
	}
	return len(p), nil
}

func hasSkillMD(files []skillFile) bool {
	for _, f := range files {
		if f.meta.Path == SkillFile && f.mode.IsRegular() {
			return true
		}
	}
	return false
}

// scanSkillFile runs the rules that apply to one entry and marks it scanned.
func scanSkillFile(f skillFile, opts Options) []schemas.AdmitScanFinding {
	m := &f.meta
	switch {
	case m.Symlink != "":
		return []schemas.AdmitScanFinding{{Rule: RuleSymlink, Severity: schemas.AdmitScanSeverityBlock, File: m.Path, Excerpt: Visible(m.Symlink), Detail: "symlink to " + Visible(m.Symlink) + "; the adapter refuses links in a skill folder because one could carry a host file into the container"}}
	case !f.mode.IsRegular():
		return []schemas.AdmitScanFinding{{Rule: RuleSpecialFile, Severity: schemas.AdmitScanSeverityBlock, File: m.Path, Detail: m.Note}}
	}
	var out []schemas.AdmitScanFinding
	if kind := executableKind(f.data); kind != "" && kind != "a script with a #! line" {
		m.Binary = true
		sev := schemas.AdmitScanSeverityInfo
		rule := RuleBinary
		if strings.HasSuffix(kind, "executable") {
			sev, rule = schemas.AdmitScanSeverityWarn, RuleExecutable
		}
		return append(out, schemas.AdmitScanFinding{Rule: rule, Severity: sev, File: m.Path, Detail: "binary file: " + kind + "; hashed, not scanned"})
	}
	if isBinary(f.data) {
		m.Binary = true
		return append(out, schemas.AdmitScanFinding{Rule: RuleBinary, Severity: schemas.AdmitScanSeverityInfo, File: m.Path, Detail: "binary file; hashed, not scanned"})
	}
	m.Scanned = true
	if m.Size > int64(len(f.data)) {
		m.Note = fmt.Sprintf("scanned first %d of %d bytes", len(f.data), m.Size)
		out = append(out, schemas.AdmitScanFinding{Rule: RuleTooLarge, Severity: schemas.AdmitScanSeverityWarn, File: m.Path, Detail: m.Note})
	}
	out = append(out, ScanText(m.Path, f.data, opts.textOptions(isMarkdown(m.Path)))...)
	if path.Base(m.Path) == "package.json" {
		out = append(out, ScanPackageJSON(m.Path, f.data)...)
	}
	return out
}

// isBinary reads a NUL byte or mostly invalid UTF-8 in the first 8 KiB as
// binary.
func isBinary(b []byte) bool {
	head := b[:min(len(b), 8192)]
	if bytes.IndexByte(head, 0) >= 0 {
		return true
	}
	if len(head) == 0 || utf8ValidPrefix(head) {
		return false
	}
	bad := 0
	for _, r := range string(head) {
		if r == '\ufffd' {
			bad++
		}
	}
	return bad*10 > len(head)
}

// utf8ValidPrefix allows a multi-byte rune cut at the 8 KiB boundary.
func utf8ValidPrefix(b []byte) bool {
	for i := 0; i < 4 && len(b) > 0; i++ {
		if utf8.Valid(b) {
			return true
		}
		b = b[:len(b)-1]
	}
	return false
}

func isMarkdown(p string) bool {
	switch strings.ToLower(path.Ext(p)) {
	case ".md", ".markdown", ".mdx", ".mdown":
		return true
	}
	return false
}

var (
	setupScriptRe = regexp.MustCompile(`(?i)^(?:install|setup|bootstrap|prereqs?|prerequisites?|preinstall|postinstall|init|configure|deps|dependencies)(?:[._-][^/]*)?\.(?:sh|bash|py)$`)
	fenceRe       = regexp.MustCompile("(?s)(?:^|\n)```[ \t]*(bash|sh|shell|console|zsh|terminal|)[ \t]*\n(.*?)\n```")
	setupNearRe   = regexp.MustCompile(`(?i)\b(?:install|setup|set up|prerequisites?|requirements?|before using|getting started|first run|quick ?start|dependencies)\b`)
)

// SetupStep is one thing the dynamic run executes: a setup script of the
// skill, or a shell snippet SKILL.md tells the reader to run first.
type SetupStep struct {
	Source string // the script path, or SKILL.md:<line> for a snippet
	Script string // snippet text; empty for a script file
}

// FindSetup lists the setup scripts (by name, at most three folders deep)
// and the install snippets of SKILL.md (fenced shell blocks whose preceding
// lines talk about installing or setting up), in a stable order. It is pure.
func FindSetup(files map[string][]byte) []SetupStep {
	var steps []SetupStep
	names := make([]string, 0, len(files))
	for p := range files {
		names = append(names, p)
	}
	sort.Strings(names)
	for _, p := range names {
		if strings.Count(p, "/") <= 2 && setupScriptRe.MatchString(path.Base(p)) {
			steps = append(steps, SetupStep{Source: p})
		}
	}
	md := string(files[SkillFile])
	prevEnd := 0
	for _, m := range fenceRe.FindAllStringSubmatchIndex(md, -1) {
		// The context is the prose since the previous block (at most six
		// lines) and the heading the block sits under.
		lines := strings.Split(md[prevEnd:m[0]], "\n")
		ctx := strings.Join(lines[max(0, len(lines)-6):], "\n") + "\n" + headingBefore(md, m[0])
		prevEnd = m[1]
		body := md[m[4]:m[5]]
		if !setupNearRe.MatchString(ctx) || strings.TrimSpace(body) == "" {
			continue
		}
		line := strings.Count(md[:m[4]], "\n") + 1
		steps = append(steps, SetupStep{Source: fmt.Sprintf("%s:%d", SkillFile, line), Script: stripPrompts(body)})
	}
	return steps
}

// headingBefore is the last markdown heading line before off, or "".
func headingBefore(md string, off int) string {
	lines := strings.Split(md[:off], "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		if strings.HasPrefix(strings.TrimSpace(lines[i]), "#") {
			return lines[i]
		}
	}
	return ""
}

// stripPrompts removes "$ " and "> " shell prompts so a console block runs.
func stripPrompts(s string) string {
	lines := strings.Split(s, "\n")
	for i, l := range lines {
		t := strings.TrimLeft(l, " \t")
		switch {
		case strings.HasPrefix(t, "$ "):
			lines[i] = strings.TrimPrefix(t, "$ ")
		case strings.HasPrefix(t, "> "):
			lines[i] = strings.TrimPrefix(t, "> ")
		}
	}
	return strings.Join(lines, "\n")
}

// runSkillDynamic copies the skill into the sandbox home at
// skills/<name>, runs every setup step in order in one container from the
// project folder, and turns what the watcher saw into findings.
func runSkillDynamic(ctx context.Context, name string, files []skillFile, opts Options) (*schemas.AdmitScanDynamic, []schemas.AdmitScanFinding, error) {
	dyn := &schemas.AdmitScanDynamic{Image: opts.image(), HelperImage: opts.helperImage()}
	contents := map[string][]byte{}
	var planted []PlantedFile
	size := 0
	for _, f := range files {
		if !f.mode.IsRegular() || int64(len(f.data)) < f.meta.Size {
			continue // links, special files and files past the scan cap stay out
		}
		if size += len(f.data); size > maxPlantBytes {
			return nil, nil, fmt.Errorf("skill %s is over %d bytes; too large for the sandbox copy", name, maxPlantBytes)
		}
		contents[f.meta.Path] = f.data
		planted = append(planted, PlantedFile{Path: "skills/" + name + "/" + f.meta.Path, Mode: int64(f.mode.Perm() | 0o400), Data: f.data})
	}
	steps := FindSetup(contents)
	if len(steps) == 0 {
		return dyn, []schemas.AdmitScanFinding{{Rule: RuleNoSetup, Severity: schemas.AdmitScanSeverityInfo, File: ".", Detail: "no setup script or install snippet found; nothing ran in the sandbox"}}, nil
	}
	script, commands := setupDriver(name, steps)
	for i, s := range steps {
		if s.Script != "" {
			planted = append(planted, PlantedFile{Path: fmt.Sprintf(".admitscan/snippet-%d.sh", i+1), Mode: 0o700, Data: []byte(s.Script + "\n")})
		}
	}
	dyn.Commands = commands
	sb, err := NewSandbox(ctx, SandboxConfig{Docker: opts.Docker, Logger: opts.logger(), Prefix: opts.prefix(), Image: opts.image(), HelperImage: opts.helperImage(), Files: planted})
	if err != nil {
		return nil, nil, fmt.Errorf("start sandbox: %w", err)
	}
	defer func() {
		if cerr := sb.Close(context.WithoutCancel(ctx)); cerr != nil {
			opts.logger().Warn("remove admission sandbox", "error", cerr.Error())
		}
	}()
	out, err := sb.Run(ctx, []string{"sh", "-c", script}, SandboxProject, nil, opts.wall())
	if err != nil {
		return nil, nil, fmt.Errorf("run setup in sandbox: %w", err)
	}
	obs, err := sb.Observe(ctx)
	if err != nil {
		obs = Observation{Error: err.Error()}
	}
	return dynamicRecord(dyn, sb.Canaries(), out, obs)
}

// dynamicRecord fills the dynamic section and its findings from one run.
func dynamicRecord(dyn *schemas.AdmitScanDynamic, canaries []Canary, out RunOutput, obs Observation) (*schemas.AdmitScanDynamic, []schemas.AdmitScanFinding, error) {
	dyn.Ran = true
	code := out.ExitCode
	dyn.ExitCode = &code
	dyn.TimedOut = out.TimedOut
	dyn.DurationMS = out.Duration.Milliseconds()
	dyn.OutputBytes = int64(len(out.Output))
	dyn.Observed = obs.Observed
	dyn.Error = obs.Error
	for _, c := range canaries {
		sum := sha256.Sum256([]byte(c.Value))
		dyn.Canaries = append(dyn.Canaries, schemas.AdmitScanCanary{ID: c.ID, Path: c.Path, SHA256: hex.EncodeToString(sum[:])})
	}
	for _, e := range obs.Events {
		switch e.T {
		case "open", "access", "dir_open":
			dyn.FileEvents++
		case "dns", "syn", "udp", "arp":
			dyn.NetEvents++
		}
	}
	findings := DynamicFindings(obs, canaries, out.Output)
	if out.TimedOut {
		findings = append(findings, schemas.AdmitScanFinding{Rule: RuleSetupTimeout, Severity: schemas.AdmitScanSeverityWarn, File: ".", Detail: fmt.Sprintf("setup still running after %s; killed", out.Duration.Round(1e9))})
	} else if out.ExitCode != 0 {
		findings = append(findings, schemas.AdmitScanFinding{Rule: RuleSetupFailed, Severity: schemas.AdmitScanSeverityInfo, File: ".", Detail: fmt.Sprintf("setup exited %d", out.ExitCode)})
	}
	return dyn, findings, nil
}

// setupDriver builds the sh script that runs every step in order (each
// failure is printed and the next step still runs) and the list of commands
// for the record.
func setupDriver(name string, steps []SetupStep) (string, []string) {
	skillDir := SandboxHome + "/skills/" + name
	var b strings.Builder
	b.WriteString("set +e\nrc=0\n")
	var commands []string
	for i, s := range steps {
		var cmd string
		switch {
		case s.Script != "":
			cmd = fmt.Sprintf("sh %s/.admitscan/snippet-%d.sh", SandboxHome, i+1)
		case strings.HasSuffix(s.Source, ".py"):
			cmd = "python3 " + shellQuote(skillDir+"/"+s.Source)
		default:
			cmd = "if command -v bash >/dev/null 2>&1; then bash " + shellQuote(skillDir+"/"+s.Source) + "; else sh " + shellQuote(skillDir+"/"+s.Source) + "; fi"
		}
		commands = append(commands, s.Source+": "+cmd)
		fmt.Fprintf(&b, "echo %s\n%s\ns=$?\necho \"== exit $s\"\n[ \"$s\" -ne 0 ] && rc=$s\n", shellQuote("== "+s.Source), cmd)
	}
	b.WriteString("exit $rc\n")
	return b.String(), commands
}

// shellQuote quotes s for POSIX sh.
func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}
