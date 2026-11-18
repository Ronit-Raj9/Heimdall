package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"text/tabwriter"
	"time"

	"github.com/maximhq/bifrost/bench/admitscan"
	"github.com/maximhq/bifrost/bench/lib"
	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/maximhq/bifrost/bench/utils"
)

// exitBlocked is the exit code of a scan whose subject was not admitted.
const exitBlocked = 1

const skillUsage = `usage: bifrost-bench skill scan <dir> [-allow-host host ...] [-dynamic] [-image ref] [-helper-image ref] [-wall 120s] [-override "<reason>"] [-by name] [-json] [-o report.md|report.json] [-config deploy/bench.yaml]
       bifrost-bench skill rules [-json]
Scans a skill folder before admission (docs/admission-scan.md): hidden instructions, invisible Unicode, encoded payloads, unlisted hosts and
fetch-and-run commands; with -dynamic its setup scripts run in a sandbox with canary credentials. Writes an admission record under
<bench root>/admissions/<id>/ ($BENCH_ADMISSIONS_ROOT overrides), or only the -o file when -o is given.
Exit 0 admitted (or admitted with override), 1 blocked, 4 scan error, 64 usage.`

const mcpUsage = `usage: bifrost-bench mcp scan <service config> [<service config> ...] [-allow-host host ...] [-helper-image ref] [-rug-wait 5s] [-override "<reason>"] [-by name] [-json] [-o report.md|report.json] [-config deploy/bench.yaml]
A service config is one service spec (the shape of a task's services: entry) or a task.yaml/suite.yaml whose services of role mcp are scanned.
Each server runs in a sandbox with canary credentials; the scan lists its tools, prompts and resources, checks them for poisoning and
shadowing, calls one harmless tool and lists again (rug pulls). One admission record per server; exit 1 when any is blocked.`

func init() {
	register("skill", "admission scan of an agent skill folder: skill scan <dir> | skill rules (docs/admission-scan.md)", runSkillCmd)
	register("mcp", "admission scan of an MCP server in a sandbox: mcp scan <service config> (docs/admission-scan.md)", runMCPCmd)
}

// csvFlag collects a repeatable flag; commas also separate values.
type csvFlag []string

func (s *csvFlag) String() string { return strings.Join(*s, ",") }

func (s *csvFlag) Set(v string) error {
	for _, p := range strings.Split(v, ",") {
		if p = strings.TrimSpace(p); p != "" {
			*s = append(*s, p)
		}
	}
	return nil
}

// scanFlags are the flags both scan commands share.
type scanFlags struct {
	allow       csvFlag
	helperImage *string
	override    *string
	overrideSet bool
	by          *string
	asJSON      *bool
	out         *string
	config      *string
}

func addScanFlags(fs *flag.FlagSet) *scanFlags {
	f := &scanFlags{}
	fs.Var(&f.allow, "allow-host", "host URLs may name; repeat or comma-separate; *.example.org allows subdomains")
	f.helperImage = fs.String("helper-image", "", "python3 image for the sandbox's planting, watcher and relay (default images.helper, else "+admitscan.DefaultHelperImage+")")
	f.override = fs.String("override", "", "admit despite block findings; the reason is required and recorded")
	f.by = fs.String("by", "", "who overrides (default $USER)")
	f.asJSON = fs.Bool("json", false, "print the record as JSON")
	f.out = fs.String("o", "", "write the report here (.json: the record, else markdown) instead of the admissions store")
	f.config = fs.String("config", defaultConfig, "bench config, for the bench root and images.helper")
	return f
}

func runSkillCmd(args []string) int {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, skillUsage)
		return exitUsage
	}
	switch args[0] {
	case "scan":
		return runSkillScan(args[1:])
	case "rules":
		return runRules(args[1:])
	default:
		fmt.Fprintln(os.Stderr, skillUsage)
		return exitUsage
	}
}

func runSkillScan(args []string) int {
	fs := flag.NewFlagSet("skill scan", flag.ContinueOnError)
	sf := addScanFlags(fs)
	dynamic := fs.Bool("dynamic", false, "run the skill's setup scripts and install snippets in a sandbox with canary credentials")
	image := fs.String("image", "", "image the setup scripts run in (default the helper image)")
	wall := fs.Duration("wall", admitscan.DefaultWall, "wall budget of the sandboxed setup run")
	b64 := fs.Int("base64-min", admitscan.DefaultBase64Min, "shortest base64 run decoded")
	hexMin := fs.Int("hex-min", admitscan.DefaultHexMin, "shortest hex run decoded")
	pos, err := parseInterleaved(fs, args)
	if err != nil || len(pos) != 1 {
		fmt.Fprintln(os.Stderr, skillUsage)
		return exitUsage
	}
	if code := sf.checkOverride(fs); code != 0 {
		return code
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	cfg := loadBenchConfig(*sf.config)
	opts := sf.options(cfg)
	opts.Dynamic, opts.Image, opts.Wall, opts.Base64Min, opts.HexMin = *dynamic, *image, *wall, *b64, *hexMin
	rec, err := admitscan.ScanSkill(ctx, pos[0], opts)
	if err != nil {
		return fail("skill scan: %v", err)
	}
	return sf.emit(os.Stdout, cfg, []*schemas.AdmitScanRecord{rec})
}

func runMCPCmd(args []string) int {
	if len(args) == 0 || args[0] != "scan" {
		fmt.Fprintln(os.Stderr, mcpUsage)
		return exitUsage
	}
	fs := flag.NewFlagSet("mcp scan", flag.ContinueOnError)
	sf := addScanFlags(fs)
	rugWait := fs.Duration("rug-wait", admitscan.DefaultRugWait, "wait before the last tools/list of the rug-pull check")
	pos, err := parseInterleaved(fs, args[1:])
	if err != nil || len(pos) == 0 {
		fmt.Fprintln(os.Stderr, mcpUsage)
		return exitUsage
	}
	if code := sf.checkOverride(fs); code != 0 {
		return code
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	cfg := loadBenchConfig(*sf.config)
	opts := sf.options(cfg)
	opts.Dynamic, opts.RugWait = true, *rugWait
	var recs []*schemas.AdmitScanRecord
	for _, file := range pos {
		specs, err := admitscan.LoadServiceSpecs(file)
		if err != nil {
			return fail("mcp scan: %v", err)
		}
		for _, spec := range specs {
			rec, err := admitscan.ScanMCP(ctx, spec, filepath.Dir(file), opts)
			if err != nil {
				return fail("mcp scan %s: %v", spec.ID, err)
			}
			rec.Source = file + "#" + spec.ID
			recs = append(recs, rec)
		}
	}
	return sf.emit(os.Stdout, cfg, recs)
}

func runRules(args []string) int {
	fs := flag.NewFlagSet("skill rules", flag.ContinueOnError)
	asJSON := fs.Bool("json", false, "print JSON")
	if err := fs.Parse(args); err != nil {
		return exitUsage
	}
	rules := admitscan.Rules()
	if *asJSON {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		if err := enc.Encode(rules); err != nil {
			return fail("print rules: %v", err)
		}
		return exitOK
	}
	tw := tabwriter.NewWriter(os.Stdout, 0, 4, 2, ' ', 0)
	fmt.Fprintln(tw, "RULE\tSEVERITY\tWHAT")
	for _, r := range rules {
		fmt.Fprintf(tw, "%s\t%s\t%s\n", r.ID, r.Severity, r.What)
	}
	if err := tw.Flush(); err != nil {
		return fail("print rules: %v", err)
	}
	return exitOK
}

// checkOverride refuses -override with an empty reason.
func (f *scanFlags) checkOverride(fs *flag.FlagSet) int {
	fs.Visit(func(fl *flag.Flag) {
		if fl.Name == "override" {
			f.overrideSet = true
		}
	})
	if f.overrideSet && strings.TrimSpace(*f.override) == "" {
		fmt.Fprintln(os.Stderr, "error: -override needs a reason")
		return exitUsage
	}
	return 0
}

func (f *scanFlags) options(cfg *lib.Config) admitscan.Options {
	helper := *f.helperImage
	if helper == "" && cfg != nil && cfg.Images.Helper != "" {
		helper = cfg.Images.Helper
	}
	return admitscan.Options{
		AllowHosts: f.allow, HelperImage: helper, Override: *f.override, By: *f.by,
		Docker: utils.Docker{}, Logger: utils.NewStdLogger(os.Stderr, false),
	}
}

// emit stores or writes each record, prints it, and returns 1 when any
// record is blocked.
func (f *scanFlags) emit(w io.Writer, cfg *lib.Config, recs []*schemas.AdmitScanRecord) int {
	blocked := false
	for _, rec := range recs {
		where, err := f.save(cfg, rec, len(recs) > 1)
		if err != nil {
			return fail("%v", err)
		}
		if *f.asJSON {
			b, err := admitscan.MarshalRecord(rec)
			if err != nil {
				return fail("%v", err)
			}
			if _, err := w.Write(b); err != nil {
				return fail("print record: %v", err)
			}
		} else {
			printSummary(w, rec, where)
		}
		blocked = blocked || rec.Decision == schemas.AdmitScanBlocked
	}
	if blocked {
		return exitBlocked
	}
	return exitOK
}

// save writes the -o file, or stores the record under the admissions root.
func (f *scanFlags) save(cfg *lib.Config, rec *schemas.AdmitScanRecord, many bool) (string, error) {
	if out := *f.out; out != "" {
		if many {
			ext := filepath.Ext(out)
			out = strings.TrimSuffix(out, ext) + "-" + rec.Subject + ext
		}
		if strings.EqualFold(filepath.Ext(out), ".json") {
			return out, admitscan.WriteJSON(out, rec)
		}
		if err := os.WriteFile(out, admitscan.RenderMarkdown(rec), 0o644); err != nil {
			return "", fmt.Errorf("write report: %w", err)
		}
		return out, nil
	}
	root := ""
	if cfg != nil {
		root = cfg.Root
	}
	adm := admitscan.AdmissionsRoot(root)
	if adm == "" {
		return "", errors.New("no admissions root: set " + admitscan.AdmissionsRootEnv + ", pass -config, or write the report with -o")
	}
	return admitscan.StoreRecord(adm, rec)
}

func printSummary(w io.Writer, rec *schemas.AdmitScanRecord, where string) {
	fmt.Fprintf(w, "%s %s: %s (%d block, %d warn, %d info) %s\n", rec.Kind, rec.Subject, strings.ToUpper(rec.Decision), rec.Counts.Block, rec.Counts.Warn, rec.Counts.Info, rec.ID)
	for _, f := range rec.Findings {
		if f.Severity == schemas.AdmitScanSeverityInfo {
			continue
		}
		loc := f.File
		if f.Line > 0 {
			loc = fmt.Sprintf("%s:%d:%d", f.File, f.Line, f.Column)
		}
		fmt.Fprintf(w, "  %-5s %-26s %s  %s\n", f.Severity, f.Rule, admitscan.Visible(loc), f.Detail)
		if f.Decoded != "" {
			fmt.Fprintf(w, "        decoded: %s\n", admitscan.Visible(f.Decoded))
		}
	}
	if rec.Override != nil {
		fmt.Fprintf(w, "  override by %s at %s: %s\n", rec.Override.By, rec.Override.At.Format(time.RFC3339), rec.Override.Reason)
	}
	fmt.Fprintf(w, "  content %s\n  record  %s\n", rec.ContentHash, where)
}

// loadBenchConfig loads the bench config when it exists; the scan runs
// without one (no store root, the default helper image).
func loadBenchConfig(path string) *lib.Config {
	if _, err := os.Stat(path); err != nil {
		return nil
	}
	cfg, err := lib.LoadConfig(path)
	if err != nil {
		fmt.Fprintf(os.Stderr, "warning: bench config %s: %v\n", path, err)
		return nil
	}
	return cfg
}

// parseInterleaved parses flags that may come before or after positional
// arguments (`skill scan <dir> -dynamic`), and returns the positionals.
func parseInterleaved(fs *flag.FlagSet, args []string) ([]string, error) {
	var pos []string
	for {
		if err := fs.Parse(args); err != nil {
			return nil, err
		}
		rest := fs.Args()
		if len(rest) == 0 {
			return pos, nil
		}
		if rest[0] == "--" {
			return append(pos, rest[1:]...), nil
		}
		pos = append(pos, rest[0])
		args = rest[1:]
	}
}
