package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/maximhq/bifrost/bench/handlers"
	"github.com/maximhq/bifrost/bench/release"
	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/maximhq/bifrost/bench/store"
	"github.com/maximhq/bifrost/bench/utils"
)

// exitRefused is the exit code of a refused release or a log that does not
// verify: the evidence said no, nothing broke.
const exitRefused = 1

const releaseUsage = `usage: bifrost-bench release create -exp <id> -approver <name> [-note text] [-override reason] [-analysis <id>] [-config deploy/bench.yaml]
       bifrost-bench release list [-config deploy/bench.yaml]
       bifrost-bench release verify [-config deploy/bench.yaml]
       bifrost-bench release export <release_id> [-o path] [-compliance] [-config deploy/bench.yaml]
The releases root is $` + release.RootEnv + `, else <bench root>/releases.`

func init() {
	register("release", "record a release on its evidence (hash-chained log): release create|list|verify|export", runRelease)
}

func runRelease(args []string) int {
	if len(args) < 1 {
		fmt.Fprintln(os.Stderr, releaseUsage)
		return exitUsage
	}
	switch args[0] {
	case "create":
		return runReleaseCreate(args[1:])
	case "list":
		return runReleaseList(args[1:])
	case "verify":
		return runReleaseVerify(args[1:])
	case "export":
		return runReleaseExport(args[1:])
	default:
		fmt.Fprintf(os.Stderr, "unknown release subcommand %q\n%s\n", args[0], releaseUsage)
		return exitUsage
	}
}

func runReleaseCreate(args []string) int {
	fs := flag.NewFlagSet("release create", flag.ContinueOnError)
	config := fs.String("config", defaultConfig, "bench config")
	exp := fs.String("exp", "", "experiment id")
	approver := fs.String("approver", "", "who approves this release")
	note := fs.String("note", "", "free-text note recorded with the release")
	override := fs.String("override", "", "reason to release against a non-ship action or a policy block; recorded")
	analysis := fs.String("analysis", "", "analysis id (default: the newest)")
	if err := fs.Parse(args); err != nil || fs.NArg() != 0 || *exp == "" {
		fmt.Fprintln(os.Stderr, releaseUsage)
		return exitUsage
	}
	l, err := openReleaseLog(*config)
	if err != nil {
		return fail("%v", err)
	}
	rec, err := l.Create(context.Background(), &schemas.ReleaseRequest{
		ExpID: *exp, AnalysisID: *analysis, Approver: *approver, Note: *note, Override: *override,
	})
	var refused *release.RefusedError
	if errors.As(err, &refused) {
		fmt.Fprintln(os.Stderr, err.Error())
		return exitRefused
	}
	if err != nil {
		return fail("%v", err)
	}
	fmt.Printf("released %s: experiment %s, analysis %s, arm %s, agent config %s (%s)\n",
		rec.ReleaseID, rec.ExpID, rec.AnalysisID, rec.Arm, rec.AgentConfigID, short(rec.ConfigHash))
	fmt.Printf("verdict %s, action %s, policy %s; approver %s\n", rec.Decision, rec.Action, orNone(string(rec.PolicyDecision)), rec.Approver)
	if rec.Override != "" {
		fmt.Printf("OVERRIDE: %s\n", rec.Override)
	}
	fmt.Printf("evidence %s/%s (sha256 %s)\nhash %s\n", l.Root(), rec.EvidencePath, rec.EvidenceSHA256, rec.Hash)
	return exitOK
}

func runReleaseList(args []string) int {
	fs := flag.NewFlagSet("release list", flag.ContinueOnError)
	config := fs.String("config", defaultConfig, "bench config")
	if err := fs.Parse(args); err != nil || fs.NArg() != 0 {
		fmt.Fprintln(os.Stderr, releaseUsage)
		return exitUsage
	}
	l, err := openReleaseLog(*config)
	if err != nil {
		return fail("%v", err)
	}
	list, err := l.List(context.Background())
	if err != nil {
		return fail("%v", err)
	}
	tw := tabwriter.NewWriter(os.Stdout, 0, 4, 2, ' ', 0)
	fmt.Fprintln(tw, "CREATED\tRELEASE\tEXPERIMENT\tCONFIG\tHASH\tDECISION\tACTION\tPOLICY\tAPPROVER\tOVERRIDE")
	for i := len(list.Releases) - 1; i >= 0; i-- {
		r := list.Releases[i]
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\n", r.CreatedAt.Format(time.RFC3339), r.ReleaseID, r.ExpID,
			r.AgentConfigID, short(r.ConfigHash), r.Decision, r.Action, orNone(string(r.PolicyDecision)), r.Approver, orNone(r.Override))
	}
	if err := tw.Flush(); err != nil {
		return fail("%v", err)
	}
	if !list.ChainOK {
		fmt.Fprintln(os.Stderr, "chain: BROKEN; run `bifrost-bench release verify` for the first bad line")
		return exitOK
	}
	fmt.Printf("chain: ok (%d records)\n", len(list.Releases))
	return exitOK
}

func runReleaseVerify(args []string) int {
	fs := flag.NewFlagSet("release verify", flag.ContinueOnError)
	config := fs.String("config", defaultConfig, "bench config")
	if err := fs.Parse(args); err != nil || fs.NArg() != 0 {
		fmt.Fprintln(os.Stderr, releaseUsage)
		return exitUsage
	}
	l, err := openReleaseLog(*config)
	if err != nil {
		return fail("%v", err)
	}
	n, err := l.Verify(context.Background())
	if errors.Is(err, release.ErrChainBroken) || errors.Is(err, release.ErrEvidenceMismatch) || errors.Is(err, schemas.ErrNotFound) {
		fmt.Fprintf(os.Stderr, "release log does not verify: %v\n", err)
		return exitRefused
	}
	if err != nil {
		return fail("%v", err)
	}
	fmt.Printf("release log ok: %d records; the chain and every evidence pack verify\n", n)
	return exitOK
}

func runReleaseExport(args []string) int {
	fs := flag.NewFlagSet("release export", flag.ContinueOnError)
	config := fs.String("config", defaultConfig, "bench config")
	out := fs.String("o", "", "copy the evidence pack to this path (default: print its path)")
	withCompliance := fs.Bool("compliance", false, "also write compliance-<release_id>.md and .json beside the export (docs/compliance.md)") // Eval integrity area (S2)
	var id string
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		id, args = args[0], args[1:]
	}
	if err := fs.Parse(args); err != nil {
		fmt.Fprintln(os.Stderr, releaseUsage)
		return exitUsage
	}
	if id == "" && fs.NArg() == 1 {
		id = fs.Arg(0)
	} else if id == "" || fs.NArg() != 0 {
		fmt.Fprintln(os.Stderr, releaseUsage)
		return exitUsage
	}
	l, err := openReleaseLog(*config)
	if err != nil {
		return fail("%v", err)
	}
	if *out == "" {
		p, err := l.Export(context.Background(), id)
		if err != nil {
			return fail("%v", err)
		}
		fmt.Println(p)
		return exportCompliance(*withCompliance, *config, l, id, "") // Eval integrity area (S2)
	}
	b, err := l.Evidence(context.Background(), id)
	if err != nil {
		return fail("%v", err)
	}
	if err := os.WriteFile(*out, b, 0o644); err != nil {
		return fail("write %s: %v", *out, err)
	}
	fmt.Printf("wrote %s (%d bytes, sha256 %s)\n", *out, len(b), utils.SHA256Bytes(b))
	return exportCompliance(*withCompliance, *config, l, id, *out) // Eval integrity area (S2)
}

// exportCompliance writes the release's compliance pack when asked
// (Eval integrity area, S2; compliance.go).
func exportCompliance(on bool, config string, l *release.Log, id, out string) int {
	if !on {
		return exitOK
	}
	ctx := context.Background()
	rec, err := l.Get(ctx, id)
	if err != nil {
		return fail("%v", err)
	}
	paths, err := releaseCompliance(ctx, config, rec, out)
	if err != nil {
		return fail("compliance pack: %v", err)
	}
	for _, p := range paths {
		fmt.Printf("wrote %s\n", p)
	}
	return exitOK
}

// openReleaseLog opens the release log over the configured run store.
func openReleaseLog(configPath string) (*release.Log, error) {
	cfg, err := loadConfig(configPath)
	if err != nil {
		return nil, err
	}
	logger := utils.NewStdLogger(os.Stderr, false)
	st, err := store.NewFSStore(cfg.Path(cfg.RunsRoot), logger)
	if err != nil {
		return nil, fmt.Errorf("open run store: %w", err)
	}
	return release.New(release.Deps{Store: st, Root: release.DefaultRoot(cfg.Root), Logger: logger})
}

// serveReleaseLog is the release log the server uses. It returns a nil
// interface, never a typed nil, when the log cannot be opened, so the release
// routes answer 503 instead of failing the whole server.
func serveReleaseLog(a *app) handlers.ReleaseLog {
	l, err := release.New(release.Deps{Store: a.store, Root: release.DefaultRoot(a.cfg.Root), Logger: a.logger})
	if err != nil {
		a.logger.Warn("release log unavailable", "error", err.Error())
		return nil
	}
	return l
}

// orNone shows an empty optional field as "none".
func orNone(s string) string {
	if s == "" {
		return "none"
	}
	return s
}
