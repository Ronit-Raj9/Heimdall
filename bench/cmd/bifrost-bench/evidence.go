package main

// Evidence storage area (P6): `bifrost-bench evidence ...` and the server
// hook that mirrors sealed experiments in the background
// (docs/evidence-storage.md).

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"text/tabwriter"
	"time"

	"github.com/maximhq/bifrost/bench/db"
	"github.com/maximhq/bifrost/bench/evidencestore"
	"github.com/maximhq/bifrost/bench/evidencestore/s3blob"
	"github.com/maximhq/bifrost/bench/lib"
	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/maximhq/bifrost/bench/utils"
)

func init() {
	register("evidence", "copy sealed evidence to a mirror and back, verify it, archive old experiments, back up the database: evidence mirror|verify|restore|backup|archive|list|fetch-backup", runEvidence)
}

const evidenceUsage = `usage: bifrost-bench evidence <sub> [flags]
  mirror        [-org id] [-exp id] [-repair]       mirror sealed experiments (all when -exp is empty);
                                                    -repair also re-sends listed objects the bucket lost
  verify        -org id [-exp id|all] [-deep]       compare the mirror with the local folders (exit 1 on a difference)
  restore       -org id -exp id | [-org id] -all [-to dir]
                                                    download, verify every seal and the chain, move into place
                                                    (-all without -org: every organization in the mirror)
  backup        [-out dir] [-no-db] [-no-mirror] [-repair]
                                                    database backup + mirror sweep + backup upload (the nightly runbook)
  archive       [-org id] [-exp id] [-days n] [-dry-run]
                                                    archive idle experiments to the mirror after a restore drill, keep a stub
  list          [-org id]                           mirrored, local and archived experiments, database backups
  fetch-backup  -name file [-out dir]               download one database backup, checked against its sha256
common flags: -config deploy/bench.yaml  -mirror file:///dir|/dir|s3://bucket/prefix  -json
The mirror comes from storage.mirror_dir or storage.s3 in the config unless -mirror is given.
S3 endpoint and keys are read from the environment variables storage.s3 names
(default HEIMDALL_S3_ENDPOINT, HEIMDALL_S3_ACCESS_KEY, HEIMDALL_S3_SECRET_KEY).`

type evidenceFlags struct {
	config, mirror, org, exp, to, out, name         string
	deep, all, dryRun, noDB, noMirror, json, repair bool
	days                                            int
}

func runEvidence(args []string) int {
	if len(args) == 0 || args[0] == "-h" || args[0] == "--help" {
		fmt.Fprintln(os.Stderr, evidenceUsage)
		return exitUsage
	}
	sub := args[0]
	var f evidenceFlags
	fset := flag.NewFlagSet("evidence "+sub, flag.ContinueOnError)
	fset.StringVar(&f.config, "config", defaultConfig, "bench config")
	fset.StringVar(&f.mirror, "mirror", "", "mirror location, overriding the config: file:///dir, /dir or s3://bucket/prefix")
	fset.StringVar(&f.org, "org", "", "organization id (local mode: local)")
	fset.StringVar(&f.exp, "exp", "", "experiment id")
	fset.StringVar(&f.to, "to", "", "restore: a separate folder for a verified copy, leaving the live store alone")
	fset.StringVar(&f.out, "out", "", "backup, fetch-backup: folder for the database backup file")
	fset.StringVar(&f.name, "name", "", "fetch-backup: backup file name (see evidence list)")
	fset.BoolVar(&f.deep, "deep", false, "verify: download and hash every object")
	fset.BoolVar(&f.repair, "repair", false, "mirror, backup: also check every listed object and re-send any the mirror lost")
	fset.BoolVar(&f.all, "all", false, "restore: every experiment the mirror holds for the org that is absent locally")
	fset.BoolVar(&f.dryRun, "dry-run", false, "archive: report, change nothing")
	fset.BoolVar(&f.noDB, "no-db", false, "backup: skip the database")
	fset.BoolVar(&f.noMirror, "no-mirror", false, "backup: skip the mirror sweep")
	fset.BoolVar(&f.json, "json", false, "print JSON")
	fset.IntVar(&f.days, "days", -1, "archive: idle days before archiving, overriding the org setting "+evidencestore.RetentionSettingKey)
	if err := fset.Parse(args[1:]); err != nil || fset.NArg() != 0 {
		fmt.Fprintln(os.Stderr, evidenceUsage)
		return exitUsage
	}
	ctx := context.Background()
	cfg, err := lib.LoadConfig(f.config)
	if err != nil {
		return fail("%v", err)
	}
	logger := utils.NewStdLogger(os.Stderr, false)
	env := &evidenceEnv{cfg: cfg, logger: logger, f: f, out: os.Stdout}
	switch sub {
	case "mirror":
		return env.mirrorCmd(ctx)
	case "verify":
		return env.verifyCmd(ctx)
	case "restore":
		return env.restoreCmd(ctx)
	case "backup":
		return env.backupCmd(ctx)
	case "archive":
		return env.archiveCmd(ctx)
	case "list":
		return env.listCmd(ctx)
	case "fetch-backup":
		return env.fetchBackupCmd(ctx)
	}
	fmt.Fprintln(os.Stderr, evidenceUsage)
	return exitUsage
}

type evidenceEnv struct {
	cfg    *lib.Config
	logger schemas.Logger
	f      evidenceFlags
	out    io.Writer
}

// evidenceRunsRoot is the folder organizations live under: storage.root
// when set, else runs_root. Local mode's org "local" is the folder itself;
// hosted orgs are <root>/orgs/<org_id>/.
func evidenceRunsRoot(cfg *lib.Config) string {
	if cfg.Storage.Root != "" {
		return cfg.Path(cfg.Storage.Root)
	}
	return cfg.Path(cfg.RunsRoot)
}

// openEvidenceMirror builds the mirror from -mirror or the storage section.
func openEvidenceMirror(cfg *lib.Config, override string, logger schemas.Logger) (*evidencestore.Mirror, error) {
	sc := cfg.Storage
	if err := sc.Validate(); err != nil {
		return nil, err
	}
	if override != "" {
		sc.MirrorDir, sc.S3.Bucket, sc.S3.Prefix = "", "", ""
		switch {
		case strings.HasPrefix(override, "s3://"):
			bucket, prefix, _ := strings.Cut(strings.TrimPrefix(override, "s3://"), "/")
			sc.S3.Bucket, sc.S3.Prefix = bucket, prefix
		case strings.HasPrefix(override, "file://"):
			sc.MirrorDir = strings.TrimPrefix(override, "file://")
		default:
			sc.MirrorDir = override
		}
	}
	var blob evidencestore.Blob
	switch {
	case sc.MirrorDir != "":
		d, err := evidencestore.NewDir(cfg.Path(sc.MirrorDir))
		if err != nil {
			return nil, err
		}
		blob = d
	case sc.S3.Bucket != "":
		sec, err := sc.S3.Secrets(nil)
		if err != nil {
			return nil, err
		}
		c, err := s3blob.New(s3blob.Config{
			Endpoint: sec.Endpoint, Region: sc.S3.RegionOrDefault(), Bucket: sc.S3.Bucket, Prefix: sc.S3.Prefix,
			AccessKey: sec.AccessKey, SecretKey: sec.SecretKey, SessionToken: sec.SessionToken,
			Addressing: sc.S3.Addressing, ChecksumSHA256: sc.S3.ChecksumSHA256,
		})
		if err != nil {
			return nil, err
		}
		blob = c
	default:
		return nil, errors.New("no mirror configured: set storage.mirror_dir or storage.s3 in the config, or pass -mirror")
	}
	m := evidencestore.NewMirror(blob, logger)
	m.Concurrency = sc.Concurrency
	return m, nil
}

func (e *evidenceEnv) mirrorStore() (*evidencestore.Mirror, error) {
	return openEvidenceMirror(e.cfg, e.f.mirror, e.logger)
}

func (e *evidenceEnv) orgs() ([]evidencestore.Org, error) {
	root := evidenceRunsRoot(e.cfg)
	if e.f.org != "" {
		o, err := evidencestore.FindOrg(root, e.f.org, e.logger)
		if err != nil {
			return nil, err
		}
		return []evidencestore.Org{o}, nil
	}
	return evidencestore.DiscoverOrgs(root, e.logger)
}

func (e *evidenceEnv) needOrg() (evidencestore.Org, error) {
	if e.f.org == "" {
		return evidencestore.Org{}, errors.New("-org is required (local mode: -org local)")
	}
	return evidencestore.FindOrg(evidenceRunsRoot(e.cfg), e.f.org, e.logger)
}

func (e *evidenceEnv) printJSON(v any) {
	enc := json.NewEncoder(e.out)
	enc.SetIndent("", "  ")
	_ = enc.Encode(v)
}

func (e *evidenceEnv) mirrorCmd(ctx context.Context) int {
	m, err := e.mirrorStore()
	if err != nil {
		return fail("%v", err)
	}
	opts := evidencestore.MirrorOptions{Repair: e.f.repair}
	var items []evidencestore.SweepItem
	var sweepErr error
	if e.f.exp != "" {
		org, err := e.needOrg()
		if err != nil {
			return fail("%v", err)
		}
		res, err := m.ExperimentWith(ctx, org, e.f.exp, opts)
		item := evidencestore.SweepItem{OrgID: org.ID, ExpID: e.f.exp, Result: res}
		if err != nil {
			item.Error, sweepErr = err.Error(), err
		}
		items = append(items, item)
	} else {
		orgs, err := e.orgs()
		if err != nil {
			return fail("%v", err)
		}
		items, sweepErr = m.SweepWith(ctx, orgs, opts)
	}
	if e.f.json {
		e.printJSON(items)
	} else {
		printSweep(e.out, m.Blob.String(), items)
	}
	if sweepErr != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", sweepErr)
		return exitInfra
	}
	return exitOK
}

func printSweep(w io.Writer, mirror string, items []evidencestore.SweepItem) {
	tw := tabwriter.NewWriter(w, 0, 2, 2, ' ', 0)
	fmt.Fprintf(tw, "mirror %s\nORG\tEXPERIMENT\tFILES\tSENT\tRESUMED\tSEALS\tPENDING\tGEN\tLISTING\tSTATUS\n", mirror)
	for _, it := range items {
		if it.Error != "" {
			fmt.Fprintf(tw, "%s\t%s\t\t\t\t\t\t\t\tFAILED: %s\n", it.OrgID, it.ExpID, it.Error)
			continue
		}
		r := it.Result
		status := "mirrored"
		if r.Unchanged {
			status = "up to date"
		}
		if r.Repaired > 0 {
			status += fmt.Sprintf(", %d repaired", r.Repaired)
		}
		if len(r.Skipped) > 0 {
			status += fmt.Sprintf(", %d skipped (%s)", len(r.Skipped), r.Skipped[0])
		}
		fmt.Fprintf(tw, "%s\t%s\t%d\t%d\t%d\t%d\t%d\t%d\t%s\t%s\n", r.OrgID, r.ExpID, r.Files, r.Uploaded, r.Resumed, r.Seals, len(r.Pending), r.Generation, short(r.ListingSHA256), status)
	}
	_ = tw.Flush()
}

func (e *evidenceEnv) verifyCmd(ctx context.Context) int {
	m, err := e.mirrorStore()
	if err != nil {
		return fail("%v", err)
	}
	org, err := e.needOrg()
	if err != nil {
		return fail("%v", err)
	}
	ids := []string{e.f.exp}
	if e.f.exp == "" || e.f.exp == "all" {
		if ids, err = mirroredOrLocal(ctx, m, org); err != nil {
			return fail("%v", err)
		}
	}
	var reports []*evidencestore.VerifyReport
	bad := false
	for _, id := range ids {
		rep, err := m.Verify(ctx, org, id, e.f.deep)
		if err != nil {
			if errors.Is(err, evidencestore.ErrNotFound) {
				rep = &evidencestore.VerifyReport{OrgID: org.ID, ExpID: id, Mirror: m.Blob.String(), Missing: []string{"the whole experiment: not mirrored"}}
			} else {
				return fail("%v", err)
			}
		}
		bad = bad || !rep.OK()
		reports = append(reports, rep)
	}
	if e.f.json {
		e.printJSON(reports)
	} else {
		for _, r := range reports {
			status := "OK"
			if !r.OK() {
				status = "MISMATCH"
			}
			fmt.Fprintf(e.out, "%s/%s generation %d listing %s: %s (%s)\n", r.OrgID, r.ExpID, r.Generation, short(r.ListingSHA256), status, r.Summary())
			for _, group := range []struct {
				name  string
				items []string
			}{{"missing", r.Missing}, {"mismatched", r.Mismatched}, {"extra", r.Extra}, {"unlisted", r.Unlisted}, {"skipped", r.Skipped}} {
				for _, it := range group.items {
					fmt.Fprintf(e.out, "  %-10s %s\n", group.name, it)
				}
			}
			if r.Local != "" {
				fmt.Fprintf(e.out, "  local      %s\n", r.Local)
			}
		}
	}
	if bad {
		return 1
	}
	return exitOK
}

// mirroredOrLocal lists every experiment the org has locally (live or
// archived) or in the mirror.
func mirroredOrLocal(ctx context.Context, m *evidencestore.Mirror, org evidencestore.Org) ([]string, error) {
	seen := map[string]bool{}
	local, err := org.Store.ListExperiments(ctx)
	if err != nil {
		return nil, err
	}
	for _, id := range local {
		seen[id] = true
	}
	stubs, err := evidencestore.ListArchived(org.Store.Root())
	if err != nil {
		return nil, err
	}
	for _, s := range stubs {
		seen[s.ExpID] = true
	}
	mirrored, err := evidencestore.MirroredExperiments(ctx, m.Blob, org.ID)
	if err != nil {
		return nil, err
	}
	for _, id := range mirrored {
		seen[id] = true
	}
	return sortedSet(seen), nil
}

func sortedSet(s map[string]bool) []string {
	out := make([]string, 0, len(s))
	for k := range s {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func (e *evidenceEnv) restoreCmd(ctx context.Context) int {
	m, err := e.mirrorStore()
	if err != nil {
		return fail("%v", err)
	}
	if (e.f.exp == "") == !e.f.all {
		return fail("restore needs -exp id or -all")
	}
	if e.f.exp != "" && e.f.org == "" {
		return fail("-exp needs -org (local mode: -org local)")
	}
	// After a disk loss the organization folders are gone: -all without
	// -org restores every organization the mirror holds, creating folders.
	orgIDs := []string{e.f.org}
	if e.f.org == "" {
		if orgIDs, err = evidencestore.MirroredOrgs(ctx, m.Blob); err != nil {
			return fail("%v", err)
		}
	}
	type job struct {
		org    evidencestore.Org
		expID  string
		target string
	}
	var jobs []job
	for _, orgID := range orgIDs {
		org, err := evidencestore.OpenOrg(evidenceRunsRoot(e.cfg), orgID, e.logger)
		if err != nil {
			return fail("%v", err)
		}
		target := e.f.to
		if target != "" && e.f.org == "" {
			target = filepath.Join(e.f.to, orgID)
		}
		if !e.f.all {
			jobs = append(jobs, job{org, e.f.exp, target})
			continue
		}
		mirrored, err := evidencestore.MirroredExperiments(ctx, m.Blob, org.ID)
		if err != nil {
			return fail("%v", err)
		}
		root := org.Store.Root()
		if target != "" {
			root = target
		}
		for _, id := range mirrored {
			if _, statErr := os.Stat(filepath.Join(root, id, "manifest.json")); errors.Is(statErr, fs.ErrNotExist) {
				jobs = append(jobs, job{org, id, target})
			}
		}
	}
	var results []*evidencestore.RestoreResult
	failed := 0
	for _, j := range jobs {
		res, err := m.Restore(ctx, j.org, j.expID, evidencestore.RestoreOptions{Target: j.target})
		if err != nil {
			failed++
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			continue
		}
		results = append(results, res)
		if !e.f.json {
			fmt.Fprintf(e.out, "restored %s/%s: %d files, %d bytes, %d seals verified (chain to %s), listing generation %d %s -> %s\n",
				res.OrgID, res.ExpID, res.Files, res.Bytes, res.Seals, short(res.LastSeal), res.Generation, short(res.ListingSHA256), res.Path)
			for _, w := range res.Warnings {
				fmt.Fprintf(e.out, "  warning: %s\n", w)
			}
		}
	}
	if e.f.json {
		e.printJSON(results)
	}
	if len(results) > 0 && e.f.to == "" {
		fmt.Fprintln(os.Stderr, "note: a running server sees restored experiments after its index is rebuilt (restart it, or `bifrost-bench index rebuild` for the results index)")
	}
	if failed > 0 {
		return exitInfra
	}
	return exitOK
}

// backupReport is what `evidence backup` did.
type backupReport struct {
	Database   *evidencestore.DBBackup   `json:"database,omitempty"`
	DBKey      string                    `json:"db_key,omitempty"`
	DBSkipped  string                    `json:"db_skipped,omitempty"`
	DBError    string                    `json:"db_error,omitempty"`
	Mirror     string                    `json:"mirror"`
	Experiment []evidencestore.SweepItem `json:"experiments,omitempty"`
	Failed     int                       `json:"failed"`
}

func (e *evidenceEnv) backupCmd(ctx context.Context) int {
	m, err := e.mirrorStore()
	if err != nil {
		return fail("%v", err)
	}
	rep := backupReport{Mirror: m.Blob.String()}
	// A failed database backup is reported and fails the run, but the
	// evidence sweep still happens.
	if !e.f.noDB {
		out := e.f.out
		if out == "" {
			out = filepath.Join(evidenceRunsRoot(e.cfg), ".evidence", "backups")
		}
		dbcfg := e.cfg.DatabaseConfig()
		if dbcfg.Driver == db.SQLite {
			if _, err := os.Stat(dbcfg.Path); errors.Is(err, fs.ErrNotExist) {
				rep.DBSkipped = "no SQLite database at " + dbcfg.Path
			}
		}
		if rep.DBSkipped == "" {
			if b, err := evidencestore.BackupDatabase(ctx, dbcfg, out, time.Now()); err != nil {
				rep.DBError = err.Error()
			} else {
				rep.Database = b
				if rep.DBKey, err = m.PutBackup(ctx, b); err != nil {
					rep.DBError = err.Error()
				}
			}
		}
	}
	if !e.f.noMirror {
		orgs, err := e.orgs()
		if err != nil {
			return fail("%v", err)
		}
		rep.Experiment, _ = m.SweepWith(ctx, orgs, evidencestore.MirrorOptions{Repair: e.f.repair})
		for _, it := range rep.Experiment {
			if it.Error != "" {
				rep.Failed++
			}
		}
	}
	if e.f.json {
		e.printJSON(rep)
	} else {
		if rep.DBError != "" {
			fmt.Fprintf(e.out, "database backup FAILED: %s\n", rep.DBError)
		}
		if rep.Database != nil {
			fmt.Fprintf(e.out, "database %s backup %s (%d bytes, sha256 %s, %s) -> %s\n", rep.Database.Driver, rep.Database.Path, rep.Database.Size, short(rep.Database.SHA256), rep.Database.Check, rep.DBKey)
		} else if rep.DBSkipped != "" {
			fmt.Fprintf(e.out, "database skipped: %s\n", rep.DBSkipped)
		}
		if !e.f.noMirror {
			printSweep(e.out, rep.Mirror, rep.Experiment)
		}
	}
	if rep.Failed > 0 || rep.DBError != "" {
		return exitInfra
	}
	return exitOK
}

func (e *evidenceEnv) archiveCmd(ctx context.Context) int {
	m, err := e.mirrorStore()
	if err != nil {
		return fail("%v", err)
	}
	var items []evidencestore.RetentionItem
	failed := false
	if e.f.exp != "" {
		org, err := e.needOrg()
		if err != nil {
			return fail("%v", err)
		}
		if e.f.dryRun {
			return fail("-dry-run applies to retention passes; archive a single experiment without it")
		}
		stub, err := m.Archive(ctx, org, e.f.exp)
		item := evidencestore.RetentionItem{OrgID: org.ID, ExpID: e.f.exp, Action: "archived"}
		if err != nil {
			item.Action, item.Reason, failed = "failed", err.Error(), true
		} else {
			item.Bytes, item.LastActivity = stub.Bytes, stub.LastActivity
		}
		items = append(items, item)
	} else {
		orgs, err := e.orgs()
		if err != nil {
			return fail("%v", err)
		}
		var settings evidencestore.SettingsReader
		if e.f.days < 0 {
			d, closeDB, err := e.openDBIfPresent(ctx)
			if err != nil {
				return fail("read retention settings: %v (or pass -days)", err)
			}
			if d != nil {
				defer closeDB()
				settings = d
			}
		}
		for _, org := range orgs {
			days := e.f.days
			if days < 0 {
				days = 0
				if settings != nil {
					if days, err = evidencestore.RetentionDays(ctx, settings, org.ID); err != nil {
						return fail("%v", err)
					}
				}
			}
			got, err := m.ApplyRetention(ctx, org, evidencestore.RetentionOptions{Days: days, DryRun: e.f.dryRun})
			items = append(items, got...)
			if err != nil {
				failed = true
			}
		}
	}
	if e.f.json {
		e.printJSON(items)
	} else {
		tw := tabwriter.NewWriter(e.out, 0, 2, 2, ' ', 0)
		fmt.Fprintln(tw, "ORG\tEXPERIMENT\tLAST ACTIVITY\tACTION\tBYTES\tREASON")
		for _, it := range items {
			last := ""
			if !it.LastActivity.IsZero() {
				last = it.LastActivity.Format(time.DateOnly)
			}
			fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%d\t%s\n", it.OrgID, it.ExpID, last, it.Action, it.Bytes, it.Reason)
		}
		_ = tw.Flush()
	}
	if failed {
		return exitInfra
	}
	return exitOK
}

// openDBIfPresent opens the operational database when one exists (a
// Postgres DSN, or a SQLite file already on disk); nil when there is none,
// so a read never creates an empty database.
func (e *evidenceEnv) openDBIfPresent(ctx context.Context) (*db.DB, func(), error) {
	cfg := e.cfg.DatabaseConfig()
	if cfg.Driver == db.SQLite {
		if _, err := os.Stat(cfg.Path); errors.Is(err, fs.ErrNotExist) {
			return nil, nil, nil
		}
	}
	d, err := db.Open(ctx, cfg)
	if err != nil {
		return nil, nil, err
	}
	return d, func() { _ = d.Close() }, nil
}

func (e *evidenceEnv) listCmd(ctx context.Context) int {
	m, err := e.mirrorStore()
	if err != nil {
		return fail("%v", err)
	}
	orgs, err := e.orgs()
	if err != nil {
		return fail("%v", err)
	}
	type row struct {
		OrgID     string `json:"org_id"`
		ExpID     string `json:"exp_id"`
		Local     string `json:"local"` // live, archived, absent
		Mirrored  bool   `json:"mirrored"`
		ArchiveAt string `json:"archived_at,omitempty"`
	}
	var rows []row
	for _, org := range orgs {
		ids, err := mirroredOrLocal(ctx, m, org)
		if err != nil {
			return fail("%v", err)
		}
		mirrored, err := evidencestore.MirroredExperiments(ctx, m.Blob, org.ID)
		if err != nil {
			return fail("%v", err)
		}
		inMirror := map[string]bool{}
		for _, id := range mirrored {
			inMirror[id] = true
		}
		for _, id := range ids {
			r := row{OrgID: org.ID, ExpID: id, Local: "absent", Mirrored: inMirror[id]}
			if s, err := evidencestore.ReadStub(org.Store.Root(), id); err == nil {
				r.Local, r.ArchiveAt = "archived", s.ArchivedAt.Format(time.RFC3339)
			} else if _, err := os.Stat(filepath.Join(org.Store.Root(), id, "manifest.json")); err == nil {
				r.Local = "live"
			}
			rows = append(rows, r)
		}
	}
	backups, err := m.ListBackups(ctx)
	if err != nil {
		return fail("%v", err)
	}
	if e.f.json {
		e.printJSON(map[string]any{"mirror": m.Blob.String(), "experiments": rows, "database_backups": backups})
		return exitOK
	}
	tw := tabwriter.NewWriter(e.out, 0, 2, 2, ' ', 0)
	fmt.Fprintf(tw, "mirror %s\nORG\tEXPERIMENT\tLOCAL\tMIRRORED\tARCHIVED AT\n", m.Blob.String())
	for _, r := range rows {
		fmt.Fprintf(tw, "%s\t%s\t%s\t%v\t%s\n", r.OrgID, r.ExpID, r.Local, r.Mirrored, r.ArchiveAt)
	}
	_ = tw.Flush()
	if len(backups) > 0 {
		fmt.Fprintln(e.out, "\ndatabase backups (newest first):")
		for _, b := range backups {
			fmt.Fprintf(e.out, "  %s  %s  %d bytes  sha256 %s\n", b.Name, b.CreatedAt.Format(time.RFC3339), b.Size, short(b.SHA256))
		}
	}
	return exitOK
}

func (e *evidenceEnv) fetchBackupCmd(ctx context.Context) int {
	if e.f.name == "" {
		return fail("-name is required (see evidence list)")
	}
	m, err := e.mirrorStore()
	if err != nil {
		return fail("%v", err)
	}
	out := e.f.out
	if out == "" {
		out = "."
	}
	b, err := m.FetchBackup(ctx, e.f.name, out)
	if err != nil {
		return fail("%v", err)
	}
	if e.f.json {
		e.printJSON(b)
	} else {
		fmt.Fprintf(e.out, "fetched %s (%d bytes, sha256 %s verified) -> %s\n", b.Name, b.Size, short(b.SHA256), b.Path)
	}
	return exitOK
}

// ---- server hook ----

// evidenceMirror is the background mirror of a running server, nil when no
// mirror is configured.
var (
	evidenceMu     sync.Mutex
	evidenceMirror *evidencestore.Service
	// evidenceDebounce is how long the hook waits for more seals of the
	// same burst before a pass (shortened by tests).
	evidenceDebounce = 5 * time.Second
)

// startEvidenceMirror starts the background mirror when storage.mirror_dir
// or storage.s3 is set: a sweep of every organization shortly after start
// and every storage.sweep_interval_m minutes, plus a pass after each seal
// reported through evidenceOnSeal. It stops with ctx. A mirror that can not
// be opened is logged and skipped: serving never depends on it.
func startEvidenceMirror(ctx context.Context, a *app) {
	if !a.cfg.Storage.Enabled() {
		return
	}
	if sc := a.cfg.Storage.S3; sc.Bucket != "" && a.cfg.Storage.MirrorDir == "" {
		if _, err := sc.Secrets(nil); err != nil {
			// A hosted template names the bucket; the mirror stays off
			// until the credentials are in the environment.
			a.logger.Info("evidence mirror off", "reason", err.Error())
			return
		}
	}
	m, err := openEvidenceMirror(a.cfg, "", a.logger)
	if err != nil {
		a.logger.Error("evidence mirror not started", "error", err.Error())
		return
	}
	svc := evidencestore.NewService(m, &evidencestore.RunsRootOrgs{Root: evidenceRunsRoot(a.cfg), Logger: a.logger},
		time.Duration(a.cfg.Storage.SweepMinutes())*time.Minute)
	svc.Debounce = evidenceDebounce
	svc.Start(ctx)
	evidenceMu.Lock()
	evidenceMirror = svc
	evidenceMu.Unlock()
	a.logger.Info("evidence mirror started", "mirror", m.Blob.String(), "sweep_minutes", a.cfg.Storage.SweepMinutes())
}

// evidenceOnSeal is the seal hook for the job runner and the launcher (P4,
// P3): call it when a run of orgID's experiment expID seals. It never
// blocks and does nothing when no mirror runs. Local mode's org id is
// "local".
func evidenceOnSeal(orgID, expID string) {
	evidenceMu.Lock()
	svc := evidenceMirror
	evidenceMu.Unlock()
	svc.Notify(orgID, expID)
}
