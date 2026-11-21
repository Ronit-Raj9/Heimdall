package main

// Gap lane G09: the hosted variants of the evidence verbs, confined to the
// organization's own runs folder (runs/orgs/<org_id>/, the app's store).
// They never touch another organization's folder or the server database:
//
//   evidence.verify   re-checks every seal and the seal chain of the org's
//                     experiments; with a server mirror also compares the
//                     org's mirrored copy (keys <org_id>/...).
//   evidence.archive  archives the org's idle experiments to the server
//                     mirror (needs one).
//   evidence.backup   writes a zip of the org's sealed evidence into the
//                     org's exports/evidence/ folder, downloadable from the
//                     Operate page (GET /operate/backups/{name}).
//   evidence.restore  restores one experiment of the org from the server
//                     mirror (needs one); the org id is never a parameter.
//
// None of them starts a container, so they run in the server process.

import (
	"archive/zip"
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/maximhq/bifrost/bench/evidencestore"
	"github.com/maximhq/bifrost/bench/handlers"
	"github.com/maximhq/bifrost/bench/jobs"
	"github.com/maximhq/bifrost/bench/store"
)

// Hosted evidence limits.
const (
	// hostedBackupMaxBytes caps the uncompressed size of one org backup.
	hostedBackupMaxBytes = 4 << 30
	// hostedBackupKeep is how many backup zips an org keeps; older ones go.
	hostedBackupKeep = 5
	// hostedArchiveDefaultDays is the idle threshold of a hosted archive
	// pass when the job gives none.
	hostedArchiveDefaultDays = 30
)

// errHostedNoMirror is what archive and restore answer without a mirror.
var errHostedNoMirror = errors.New("this server has no evidence mirror, so archive and restore are off; use Back up on the Operate page to download a zip of your evidence, or ask the server owner to set storage.mirror_dir or storage.s3")

func init() {
	for _, sub := range []string{"verify", "archive", "backup", "restore"} {
		registerJobKind(hostedEvidenceJob(sub))
	}
}

func hostedEvidenceJob(sub string) jobKindSpec {
	kind := "evidence." + sub
	validate := func(p operateParams) error {
		if p.Repair || p.NoDB {
			return errors.New("repair and no_db are server owner options")
		}
		if err := operateCheckID("exp", p.Exp, sub == "restore"); err != nil {
			return err
		}
		if p.Days != nil && (*p.Days < 1 || *p.Days > 3650) {
			return errors.New("days must be between 1 and 3650")
		}
		return nil
	}
	return jobKindSpec{
		Kind: kind, Hosted: true, Opts: []jobs.KindOption{jobs.WithLimit(1, 1)},
		Validate: func(ctx context.Context, env jobEnv, raw json.RawMessage) (any, string, error) {
			p, err := parseOperateParams(raw)
			if err == nil {
				err = validate(p)
			}
			if err != nil {
				return nil, "", operateInvalid(err)
			}
			target := "evidence"
			if p.Exp != "" {
				target = p.Exp
			}
			return p, target, nil
		},
		Run: func(ctx context.Context, env jobEnv, j *jobs.Job, log jobs.Logger, progress func(any)) (any, error) {
			p, err := parseOperateParams(j.Params)
			if err == nil {
				err = validate(p)
			}
			if err != nil {
				return nil, err
			}
			org, err := hostedEvidenceOrg(env)
			if err != nil {
				return nil, err
			}
			switch sub {
			case "verify":
				return hostedEvidenceVerify(ctx, env, org, p, log)
			case "archive":
				return hostedEvidenceArchive(ctx, env, org, p, log)
			case "backup":
				return hostedEvidenceBackup(ctx, env, org, p, log, time.Now().UTC())
			case "restore":
				return hostedEvidenceRestore(ctx, env, org, p, log)
			}
			return nil, fmt.Errorf("unknown evidence verb %s", sub)
		},
	}
}

// hostedEvidenceOrg is the job's organization over its own store only.
func hostedEvidenceOrg(env jobEnv) (evidencestore.Org, error) {
	if env.App == nil || env.App.store == nil {
		return evidencestore.Org{}, errors.New("no runs folder for this organization")
	}
	if env.OrgID == "" {
		return evidencestore.Org{}, errors.New("no organization for this job")
	}
	return evidencestore.Org{ID: env.OrgID, Store: env.App.store}, nil
}

// hostedMirror opens the server's mirror; nil (no error) when there is none.
func hostedMirror(env jobEnv) (*evidencestore.Mirror, error) {
	sc := env.App.cfg.Storage
	if sc.MirrorDir == "" && sc.S3.Bucket == "" {
		return nil, nil
	}
	m, err := openEvidenceMirror(env.App.cfg, "", env.App.logger)
	if err != nil {
		return nil, errors.New("the server's evidence mirror is not reachable; ask the server owner to check its storage settings")
	}
	return m, nil
}

// hostedVerifyItem is one experiment of a hosted verify.
type hostedVerifyItem struct {
	ExpID  string                      `json:"exp_id"`
	Seals  int                         `json:"seals"`
	OK     bool                        `json:"ok"`
	Error  string                      `json:"error,omitempty"`
	Mirror *evidencestore.VerifyReport `json:"mirror,omitempty"`
}

// hostedOrgExperiments is p.Exp (which must exist in the org) or every
// experiment of the org.
func hostedOrgExperiments(ctx context.Context, org evidencestore.Org, exp string) ([]string, error) {
	ids, err := org.Store.ListExperiments(ctx)
	if err != nil {
		return nil, err
	}
	sort.Strings(ids)
	if exp == "" {
		return ids, nil
	}
	for _, id := range ids {
		if id == exp {
			return []string{exp}, nil
		}
	}
	return nil, fmt.Errorf("experiment %s is not in this organization's runs folder", exp)
}

func hostedEvidenceVerify(ctx context.Context, env jobEnv, org evidencestore.Org, p operateParams, log jobs.Logger) (any, error) {
	ids, err := hostedOrgExperiments(ctx, org, p.Exp)
	if err != nil {
		return nil, err
	}
	if len(ids) == 0 {
		log.Printf("no experiments yet: run one from the Run page, then verify it here")
		return map[string]any{"ok": true, "experiments": []hostedVerifyItem{}}, nil
	}
	m, err := hostedMirror(env)
	if err != nil {
		log.Warnf("%v", err)
	}
	items := make([]hostedVerifyItem, 0, len(ids))
	bad := 0
	for _, id := range ids {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		it := hostedVerifyItem{ExpID: id}
		it.Seals, err = verifyLocalSeals(ctx, org.Store, id)
		if err != nil {
			it.Error = err.Error()
		} else {
			it.OK = true
		}
		if m != nil {
			rep, merr := m.Verify(ctx, org, id, p.Deep)
			switch {
			case errors.Is(merr, evidencestore.ErrNotFound):
				rep = &evidencestore.VerifyReport{OrgID: org.ID, ExpID: id, Missing: []string{"the whole experiment: not mirrored yet"}}
			case merr != nil:
				rep = &evidencestore.VerifyReport{OrgID: org.ID, ExpID: id, Skipped: []string{"mirror check failed: " + merr.Error()}}
			}
			rep.Mirror = "" // the server's mirror location is not shown to orgs
			it.Mirror = rep
			if !rep.OK() {
				it.OK = false
			}
		}
		if it.OK {
			log.Printf("OK        %s: %d sealed run(s), seal chain intact", id, it.Seals)
		} else {
			bad++
			detail := it.Error
			if detail == "" && it.Mirror != nil {
				detail = "mirror: " + it.Mirror.Summary()
			}
			log.Warnf("MISMATCH  %s: %s", id, detail)
		}
		items = append(items, it)
	}
	res := map[string]any{"ok": bad == 0, "experiments": items, "mirror_checked": m != nil}
	if bad > 0 {
		return res, fmt.Errorf("%d of %d experiment(s) do not verify (see the log)", bad, len(items))
	}
	return res, nil
}

// verifyLocalSeals checks the seal chain of one experiment and every sealed
// run seals.log names; it returns the number of sealed runs.
func verifyLocalSeals(ctx context.Context, st *store.FSStore, expID string) (int, error) {
	b, err := os.ReadFile(filepath.Join(st.Root(), expID, "seals.log"))
	if errors.Is(err, fs.ErrNotExist) {
		return 0, nil // nothing sealed yet
	}
	if err != nil {
		return 0, err
	}
	if err := st.VerifyChain(expID); err != nil {
		return 0, fmt.Errorf("seal chain: %w", err)
	}
	n := 0
	sc := bufio.NewScanner(bytes.NewReader(b))
	for sc.Scan() {
		f := strings.Fields(sc.Text())
		if len(f) == 0 {
			continue
		}
		if len(f) != 2 {
			return n, fmt.Errorf("seals.log line %d malformed", n+1)
		}
		if err := st.Verify(ctx, expID, f[0]); err != nil {
			return n, err
		}
		n++
	}
	return n, sc.Err()
}

func hostedEvidenceArchive(ctx context.Context, env jobEnv, org evidencestore.Org, p operateParams, log jobs.Logger) (any, error) {
	m, err := hostedMirror(env)
	if err != nil {
		return nil, err
	}
	if m == nil {
		return nil, errHostedNoMirror
	}
	if p.Exp != "" {
		if p.DryRun {
			return nil, errors.New("dry run applies to an archive pass over idle experiments; clear the experiment id or the dry run")
		}
		if _, err := hostedOrgExperiments(ctx, org, p.Exp); err != nil {
			return nil, err
		}
		stub, err := m.Archive(ctx, org, p.Exp)
		if err != nil {
			return nil, fmt.Errorf("archive %s: %w", p.Exp, err)
		}
		log.Printf("archived %s (%d bytes); a stub stays, restore brings it back", p.Exp, stub.Bytes)
		return []evidencestore.RetentionItem{{OrgID: org.ID, ExpID: p.Exp, Action: "archived", Bytes: stub.Bytes, LastActivity: stub.LastActivity}}, nil
	}
	days := hostedArchiveDefaultDays
	if p.Days != nil {
		days = *p.Days
	}
	items, err := m.ApplyRetention(ctx, org, evidencestore.RetentionOptions{Days: days, DryRun: p.DryRun})
	for _, it := range items {
		log.Printf("%-9s %s %s", it.Action, it.ExpID, it.Reason)
	}
	if len(items) == 0 {
		log.Printf("no experiment idle for more than %d days", days)
	}
	if items == nil {
		items = []evidencestore.RetentionItem{}
	}
	if err != nil {
		return items, fmt.Errorf("archive pass: %w", err)
	}
	return items, nil
}

func hostedEvidenceRestore(ctx context.Context, env jobEnv, org evidencestore.Org, p operateParams, log jobs.Logger) (any, error) {
	m, err := hostedMirror(env)
	if err != nil {
		return nil, err
	}
	if m == nil {
		return nil, errHostedNoMirror
	}
	res, err := m.Restore(ctx, org, p.Exp, evidencestore.RestoreOptions{})
	if err != nil {
		return nil, fmt.Errorf("restore %s: %w", p.Exp, err)
	}
	res.Path = p.Exp // the server path stays private
	log.Printf("restored %s: %d files, %d seals verified", res.ExpID, res.Files, res.Seals)
	for _, w := range res.Warnings {
		log.Warnf("%s", w)
	}
	return res, nil
}

// hostedBackupResult is what evidence.backup returns.
type hostedBackupResult struct {
	Name        string `json:"name"`
	Path        string `json:"path"` // project-relative
	Size        int64  `json:"size"`
	Files       int    `json:"files"`
	Experiments int    `json:"experiments"`
}

// hostedEvidenceBackup zips the org's experiments (or one) into
// exports/evidence/ of the org's project. Lease files, staging folders and
// anything outside the experiment folders stay out.
func hostedEvidenceBackup(ctx context.Context, env jobEnv, org evidencestore.Org, p operateParams, log jobs.Logger, now time.Time) (any, error) {
	ids, err := hostedOrgExperiments(ctx, org, p.Exp)
	if err != nil {
		return nil, err
	}
	if len(ids) == 0 {
		return nil, errors.New("no experiments to back up yet: run one from the Run page first")
	}
	dir := handlers.OperateBackupDir(env.ProjectRoot)
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return nil, fmt.Errorf("backup folder: %w", err)
	}
	name := handlers.OperateBackupName(org.ID, now)
	tmp, err := os.CreateTemp(dir, ".backup-*.zip")
	if err != nil {
		return nil, fmt.Errorf("backup: %w", err)
	}
	defer os.Remove(tmp.Name())
	zw := zip.NewWriter(tmp)
	var total int64
	files := 0
	root := org.Store.Root()
	for _, id := range ids {
		expDir := filepath.Join(root, id)
		err := filepath.WalkDir(expDir, func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if err := ctx.Err(); err != nil {
				return err
			}
			if d.Type()&fs.ModeSymlink != 0 {
				return nil // never follow a link out of the org folder
			}
			if d.IsDir() {
				if path != expDir && strings.HasPrefix(d.Name(), ".") {
					return filepath.SkipDir
				}
				return nil
			}
			if !d.Type().IsRegular() || strings.HasSuffix(d.Name(), ".lock") || strings.HasPrefix(d.Name(), ".lease") {
				return nil
			}
			info, err := d.Info()
			if err != nil {
				return err
			}
			if total += info.Size(); total > hostedBackupMaxBytes {
				return fmt.Errorf("the evidence is larger than %d GiB; back up one experiment at a time", hostedBackupMaxBytes>>30)
			}
			rel, err := filepath.Rel(root, path)
			if err != nil {
				return err
			}
			h, err := zip.FileInfoHeader(info)
			if err != nil {
				return err
			}
			h.Name, h.Method = filepath.ToSlash(rel), zip.Deflate
			w, err := zw.CreateHeader(h)
			if err != nil {
				return err
			}
			f, err := os.Open(path)
			if err != nil {
				return err
			}
			_, err = io.Copy(w, f)
			f.Close()
			files++
			return err
		})
		if err != nil {
			zw.Close()
			tmp.Close()
			return nil, fmt.Errorf("backup %s: %w", id, err)
		}
		log.Printf("added %s", id)
	}
	if err := zw.Close(); err != nil {
		tmp.Close()
		return nil, fmt.Errorf("backup: %w", err)
	}
	info, err := tmp.Stat()
	if cerr := tmp.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return nil, fmt.Errorf("backup: %w", err)
	}
	dst := filepath.Join(dir, name)
	if err := os.Rename(tmp.Name(), dst); err != nil {
		return nil, fmt.Errorf("backup: %w", err)
	}
	pruneHostedBackups(dir, log)
	log.Printf("wrote %s (%d files, %d bytes); download it from the Operate page", name, files, info.Size())
	rel, _ := filepath.Rel(env.ProjectRoot, dst)
	return hostedBackupResult{Name: name, Path: filepath.ToSlash(rel), Size: info.Size(), Files: files, Experiments: len(ids)}, nil
}

// pruneHostedBackups keeps the newest hostedBackupKeep backups.
func pruneHostedBackups(dir string, log jobs.Logger) {
	list, err := handlers.ListOperateBackups(filepath.Dir(filepath.Dir(dir)))
	if err != nil || len(list) <= hostedBackupKeep {
		return
	}
	for _, b := range list[hostedBackupKeep:] {
		if os.Remove(filepath.Join(dir, b.Name)) == nil {
			log.Printf("removed the old backup %s (an org keeps %d)", b.Name, hostedBackupKeep)
		}
	}
}
