package evidencestore

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/maximhq/bifrost/bench/store"
	"github.com/maximhq/bifrost/bench/utils"
)

// Fixed names of the run folder layout (DESIGN.md 16.1) this package reads.
// They mirror the store's own constants, which are unexported.
const (
	manifestFile   = "manifest.json"
	sealsLogFile   = "seals.log"
	reviewsLogFile = "reviews.log"
	sealFile       = "seal.json"
	slotFile       = "slot.json"
	slotsDir       = "slots"
	runsDir        = "runs"

	// listingName is the mirror's own record of an experiment, beside its
	// files. A leading dot keeps it apart from every name the store writes.
	listingName = ".mirror.json"
	// stateDir holds this package's working folders inside a store root:
	// locks, restore staging and folders being removed. Its name is not a
	// valid experiment id, so the store never lists it.
	stateDir = ".evidence"
)

// ErrRefused is returned when an experiment fails a seal, chain or
// immutability check and nothing is copied or moved.
var ErrRefused = errors.New("evidencestore: refused")

// ErrArchived is returned for an experiment whose local folder is an archive
// stub.
var ErrArchived = errors.New("evidencestore: experiment is archived")

var idPattern = regexp.MustCompile(`^[a-z0-9_]{1,128}$`)

func validID(kind, id string) error {
	if !idPattern.MatchString(id) {
		return fmt.Errorf("invalid %s %q: want ^[a-z0-9_]+$", kind, id)
	}
	return nil
}

// Org is one organization's run store: in local mode the runs root and org
// "local", in hosted mode runs/orgs/<org_id>/.
type Org struct {
	ID    string
	Store *store.FSStore
}

func (o Org) root() string               { return o.Store.Root() }
func (o Org) expDir(expID string) string { return filepath.Join(o.Store.Root(), expID) }

// LocalOrgID is the implicit organization of local mode.
const LocalOrgID = "local"

// DiscoverOrgs lists the organizations under a runs root: "local" for the
// root itself, then every runs/orgs/<org_id>/ folder, sorted.
func DiscoverOrgs(runsRoot string, logger schemas.Logger) ([]Org, error) {
	local, err := store.NewFSStore(runsRoot, logger)
	if err != nil {
		return nil, err
	}
	out := []Org{{ID: LocalOrgID, Store: local}}
	entries, err := os.ReadDir(filepath.Join(local.Root(), "orgs"))
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return out, nil
		}
		return nil, fmt.Errorf("list orgs: %w", err)
	}
	for _, e := range entries {
		if !e.IsDir() || validID("org_id", e.Name()) != nil || e.Name() == LocalOrgID {
			continue
		}
		st, err := store.NewFSStore(filepath.Join(local.Root(), "orgs", e.Name()), logger)
		if err != nil {
			return nil, err
		}
		out = append(out, Org{ID: e.Name(), Store: st})
	}
	return out, nil
}

// FindOrg returns the organization with the given id from DiscoverOrgs.
func FindOrg(runsRoot, orgID string, logger schemas.Logger) (Org, error) {
	if err := validID("org_id", orgID); err != nil {
		return Org{}, err
	}
	if orgID == LocalOrgID {
		st, err := store.NewFSStore(runsRoot, logger)
		return Org{ID: orgID, Store: st}, err
	}
	dir := filepath.Join(runsRoot, "orgs", orgID)
	if _, err := os.Stat(dir); err != nil {
		return Org{}, fmt.Errorf("org %s: %w", orgID, err)
	}
	st, err := store.NewFSStore(dir, logger)
	return Org{ID: orgID, Store: st}, err
}

// OpenOrg returns the organization with the given id, creating its folder
// when it is missing: restore after a disk loss brings an organization back
// into an empty runs root.
func OpenOrg(runsRoot, orgID string, logger schemas.Logger) (Org, error) {
	if err := validID("org_id", orgID); err != nil {
		return Org{}, err
	}
	dir := runsRoot
	if orgID != LocalOrgID {
		dir = filepath.Join(runsRoot, "orgs", orgID)
	}
	st, err := store.NewFSStore(dir, logger)
	return Org{ID: orgID, Store: st}, err
}

// localFile is one file of an experiment chosen for the mirror.
type localFile struct {
	Path   string // relative slash path inside the experiment folder
	Size   int64
	SHA256 string
	abs    string // the file on disk, or "" when data holds the bytes
	data   []byte // a log snapshot: these bytes, not the live file
}

// snapshot is an experiment as it is about to be mirrored: verified, with
// the list of its immutable files.
type snapshot struct {
	OrgID, ExpID string
	ManifestHash string
	Files        []localFile // sorted by Path
	Seals        int
	LastSeal     string
	Pending      []string // run folders not (yet) in seals.log: in flight or never sealed
	Skipped      []string // outside sealed runs: dot names, non-regular entries, a stray stub; never mirrored
}

func (s *snapshot) file(p string) (localFile, bool) {
	i := sort.Search(len(s.Files), func(i int) bool { return s.Files[i].Path >= p })
	if i < len(s.Files) && s.Files[i].Path == p {
		return s.Files[i], true
	}
	return localFile{}, false
}

// appendOnlyLogs are the experiment-level files that only ever grow by whole
// lines: seals.log and reviews.log (store), resumes.jsonl (run -resume with
// allowed drift), controls.jsonl (pause, live settings, run cancel and
// requeue; bench/runctl). A pass reads each once, up to its last complete line, and
// the mirror accepts a new copy only when it starts with the mirrored bytes.
var appendOnlyLogs = []string{sealsLogFile, reviewsLogFile, "resumes.jsonl", "controls.jsonl"} // controls.jsonl: run control log (bench/runctl)

func isAppendOnlyLog(p string) bool {
	for _, l := range appendOnlyLogs {
		if p == l {
			return true
		}
	}
	return false
}

// maxKeyLen is the longest object key the mirror writes (S3's limit).
const maxKeyLen = 1024

// mirrorPathAllowed reports whether p (relative to the experiment folder) is
// a path a mirror pass can select. Restore accepts nothing else from a
// listing, so a planted lease, stub or dot file is refused.
func mirrorPathAllowed(p string) error {
	if err := validRelPath(p); err != nil {
		return err
	}
	parts := strings.Split(p, "/")
	inRun := len(parts) >= 5 && parts[0] == slotsDir && parts[2] == runsDir
	switch {
	case len(parts) == 1 && parts[0] == stubFile:
		return fmt.Errorf("path %q is an archive stub, never mirrored", p)
	case len(parts) == 3 && parts[0] == slotsDir && (parts[2] == "lease.json" || parts[2] == "lease.lock"):
		return fmt.Errorf("path %q is operational state, never mirrored", p)
	case len(parts) == 4 && parts[0] == slotsDir && parts[2] == runsDir:
		return fmt.Errorf("path %q: only run folders sit under runs/", p)
	}
	for i, seg := range parts {
		if strings.HasPrefix(seg, ".") && !(inRun && i >= 4) {
			return fmt.Errorf("path %q: dot name outside a sealed run, never mirrored", p)
		}
	}
	return nil
}

// takeSnapshot verifies an experiment with the store's own checks and lists
// its immutable files:
//
//   - manifest.json, whose hash field must match its content;
//   - the append-only logs (seals.log, reviews.log, resumes.jsonl) as read
//     once at the start (complete lines only), so the mirrored seals.log
//     names exactly the runs mirrored with it;
//   - every slot.json;
//   - every run folder named in that seals.log, after store.Verify and
//     store.VerifyChain pass and its seal names the manifest, with every
//     file its seal lists (dot files included);
//   - every regular file of regrades/, analysis/, reviews/ and any other
//     folder the store writes.
//
// Never included: lease.json and lease.lock (operational state), run
// folders not in the log (in flight or never sealed, reported as Pending),
// and outside sealed runs any name starting with a dot, any non-regular
// file and a stray archived.json (reported as Skipped). A seal, chain or
// manifest failure, a file directly under a runs/ folder, or a sealed file
// whose name can not be an object key returns ErrRefused and nothing is
// mirrored.
func takeSnapshot(ctx context.Context, org Org, expID string) (*snapshot, error) {
	if err := validID("org_id", org.ID); err != nil {
		return nil, err
	}
	if err := validID("exp_id", expID); err != nil {
		return nil, err
	}
	dir := org.expDir(expID)
	if _, err := os.Stat(filepath.Join(dir, manifestFile)); err != nil {
		if _, serr := os.Stat(filepath.Join(dir, stubFile)); serr == nil {
			return nil, fmt.Errorf("experiment %s/%s: %w", org.ID, expID, ErrArchived)
		}
		return nil, fmt.Errorf("experiment %s/%s: no manifest.json: %w", org.ID, expID, schemas.ErrNotFound)
	}
	logs := make(map[string][]byte, len(appendOnlyLogs))
	for _, name := range appendOnlyLogs {
		b, err := readLogSnapshot(filepath.Join(dir, name))
		if err != nil {
			return nil, err
		}
		logs[name] = b
	}
	entries, err := parseSealsLog(logs[sealsLogFile])
	if err != nil {
		return nil, fmt.Errorf("experiment %s/%s: %w: %w", org.ID, expID, err, ErrRefused)
	}
	mb, err := os.ReadFile(filepath.Join(dir, manifestFile))
	if err != nil {
		return nil, fmt.Errorf("experiment %s/%s: %w", org.ID, expID, err)
	}
	manifestHash, err := checkManifest(mb)
	if err != nil {
		return nil, fmt.Errorf("experiment %s/%s: %w: %w", org.ID, expID, err, ErrRefused)
	}
	if err := verifyChainSettled(org.Store, expID); err != nil {
		return nil, fmt.Errorf("experiment %s/%s: %w: %w", org.ID, expID, err, ErrRefused)
	}
	for _, e := range entries {
		if err := org.Store.Verify(ctx, expID, e.RunID); err != nil {
			return nil, fmt.Errorf("experiment %s/%s: %w: %w", org.ID, expID, err, ErrRefused)
		}
	}
	snap := &snapshot{OrgID: org.ID, ExpID: expID, ManifestHash: manifestHash, Seals: len(entries)}
	if len(entries) > 0 {
		snap.LastSeal = entries[len(entries)-1].Hash
	}
	logged := make(map[string]string, len(entries))
	for _, e := range entries {
		logged[e.RunID] = e.Hash
	}
	err = filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if p == dir {
			return nil
		}
		rel, err := filepath.Rel(dir, p)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		if strings.HasPrefix(d.Name(), ".") {
			snap.Skipped = append(snap.Skipped, rel)
			if d.IsDir() {
				return fs.SkipDir
			}
			return nil
		}
		parts := strings.Split(rel, "/")
		// slots/<slot>/runs/<entry>: only folders, and only runs in the log
		// snapshot.
		if len(parts) == 4 && parts[0] == slotsDir && parts[2] == runsDir {
			if !d.IsDir() {
				return fmt.Errorf("%s is not a run folder: %w", rel, ErrRefused)
			}
			hash, ok := logged[parts[3]]
			if !ok {
				snap.Pending = append(snap.Pending, parts[3])
				return fs.SkipDir
			}
			files, err := sealedRunFiles(p, rel, hash)
			if err != nil {
				return fmt.Errorf("run %s: %w: %w", parts[3], err, ErrRefused)
			}
			snap.Files = append(snap.Files, files...)
			delete(logged, parts[3])
			return fs.SkipDir
		}
		if d.IsDir() {
			return nil
		}
		if len(parts) == 3 && parts[0] == slotsDir && (parts[2] == "lease.json" || parts[2] == "lease.lock") {
			return nil
		}
		if !d.Type().IsRegular() || rel == stubFile {
			snap.Skipped = append(snap.Skipped, rel)
			return nil
		}
		if isAppendOnlyLog(rel) {
			if b := logs[rel]; len(b) > 0 {
				snap.Files = append(snap.Files, logFile(rel, b))
			}
			return nil
		}
		st, err := d.Info()
		if err != nil {
			return err
		}
		sum, err := utils.SHA256File(p)
		if err != nil {
			return err
		}
		snap.Files = append(snap.Files, localFile{Path: rel, Size: st.Size(), SHA256: sum, abs: p})
		return nil
	})
	if err != nil {
		if errors.Is(err, ErrRefused) {
			return nil, fmt.Errorf("experiment %s/%s: %w", org.ID, expID, err)
		}
		return nil, fmt.Errorf("experiment %s/%s: walk: %w", org.ID, expID, err)
	}
	if len(logged) > 0 {
		missing := make([]string, 0, len(logged))
		for id := range logged {
			missing = append(missing, id)
		}
		sort.Strings(missing)
		return nil, fmt.Errorf("experiment %s/%s: run %s is in seals.log but has no run folder: %w", org.ID, expID, missing[0], ErrRefused)
	}
	if err := checkSealsName(dir, entries, manifestHash); err != nil {
		return nil, fmt.Errorf("experiment %s/%s: %w: %w", org.ID, expID, err, ErrRefused)
	}
	keyRoom := maxKeyLen - len(expPrefix(org.ID, expID))
	for _, f := range snap.Files {
		if err := mirrorPathAllowed(f.Path); err != nil {
			return nil, fmt.Errorf("experiment %s/%s: %w: %w", org.ID, expID, err, ErrRefused)
		}
		if len(f.Path) > keyRoom {
			return nil, fmt.Errorf("experiment %s/%s: path %s is too long for an object key: %w", org.ID, expID, f.Path, ErrRefused)
		}
	}
	sort.Slice(snap.Files, func(i, j int) bool { return snap.Files[i].Path < snap.Files[j].Path })
	sort.Strings(snap.Pending)
	sort.Strings(snap.Skipped)
	return snap, nil
}

// logFile is a log snapshot as a mirror file.
func logFile(rel string, b []byte) localFile {
	return localFile{Path: rel, Size: int64(len(b)), SHA256: utils.SHA256Bytes(b), data: b}
}

// sealedRunFiles lists a verified run folder from its seal: every file the
// seal names with the hash it records, and seal.json itself, whose sha256 is
// the seal hash in the log. The sizes come from the folder; Put checks the
// bytes again on the way out, so a file changed after the check is refused.
func sealedRunFiles(dir, rel, sealHash string) ([]localFile, error) {
	b, err := os.ReadFile(filepath.Join(dir, sealFile))
	if err != nil {
		return nil, err
	}
	if utils.SHA256Bytes(b) != sealHash {
		return nil, fmt.Errorf("seal.json changed after verification")
	}
	var rec schemas.SealRecord
	if err := json.Unmarshal(b, &rec); err != nil {
		return nil, fmt.Errorf("decode seal.json: %w", err)
	}
	out := make([]localFile, 0, len(rec.Files)+1)
	out = append(out, localFile{Path: rel + "/" + sealFile, Size: int64(len(b)), SHA256: sealHash, abs: filepath.Join(dir, sealFile)})
	for name, sum := range rec.Files {
		p := filepath.Join(dir, filepath.FromSlash(name))
		st, err := os.Lstat(p)
		if err != nil {
			return nil, fmt.Errorf("file %s: %w", name, err)
		}
		if !st.Mode().IsRegular() || !isHexSHA256(sum) {
			return nil, fmt.Errorf("file %s is not a regular sealed file", name)
		}
		out = append(out, localFile{Path: rel + "/" + name, Size: st.Size(), SHA256: sum, abs: p})
	}
	return out, nil
}

// checkSealsName checks that every seal of the log names the manifest the
// experiment holds, so a manifest swapped for another one with a consistent
// hash field is refused.
func checkSealsName(dir string, entries []sealEntry, manifestHash string) error {
	if len(entries) == 0 {
		return nil
	}
	runDirs, err := filepath.Glob(filepath.Join(dir, slotsDir, "*", runsDir, "*"))
	if err != nil {
		return err
	}
	byID := make(map[string]string, len(runDirs))
	for _, d := range runDirs {
		byID[filepath.Base(d)] = d
	}
	for _, e := range entries {
		var rec schemas.SealRecord
		b, err := os.ReadFile(filepath.Join(byID[e.RunID], sealFile))
		if err != nil {
			return fmt.Errorf("run %s: %w", e.RunID, err)
		}
		if err := json.Unmarshal(b, &rec); err != nil {
			return fmt.Errorf("run %s: decode seal.json: %w", e.RunID, err)
		}
		if rec.ManifestHash != manifestHash {
			return fmt.Errorf("run %s was sealed under manifest %s, manifest.json is %s", e.RunID, short(rec.ManifestHash), short(manifestHash))
		}
	}
	return nil
}

// verifyChainSettled runs store.VerifyChain and, when it fails, once more
// after a short pause: a run that is sealing while the chain is read can
// show as sealed but not yet logged for a moment.
func verifyChainSettled(st *store.FSStore, expID string) error {
	err := st.VerifyChain(expID)
	if err == nil {
		return nil
	}
	time.Sleep(300 * time.Millisecond)
	return st.VerifyChain(expID)
}

// checkManifest returns the manifest's hash field after checking that it is
// the sha256 of the manifest's canonical JSON with the field empty, the rule
// engine.ManifestHash applies.
func checkManifest(b []byte) (string, error) {
	var m schemas.Manifest
	if err := json.Unmarshal(b, &m); err != nil {
		return "", fmt.Errorf("decode manifest.json: %w", err)
	}
	want := m.Hash
	m.Hash = ""
	got, err := utils.HashJSON(&m)
	if err != nil {
		return "", err
	}
	if got != want {
		return "", fmt.Errorf("manifest.json hashes to %s, its hash field says %s", short(got), short(want))
	}
	return want, nil
}

// readLogSnapshot reads an append-only log up to its last complete line. A
// missing log is empty.
func readLogSnapshot(p string) ([]byte, error) {
	b, err := os.ReadFile(p)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, nil
		}
		return nil, fmt.Errorf("read %s: %w", filepath.Base(p), err)
	}
	i := bytes.LastIndexByte(b, '\n')
	return b[:i+1], nil
}

// sealEntry is one seals.log line.
type sealEntry struct {
	RunID string
	Hash  string
}

// parseSealsLog parses seals.log strictly: any malformed line is an error.
func parseSealsLog(b []byte) ([]sealEntry, error) {
	var out []sealEntry
	for i, line := range bytes.Split(b, []byte{'\n'}) {
		if len(line) == 0 {
			continue
		}
		f := strings.Fields(string(line))
		if len(f) != 2 || validID("run_id", f[0]) != nil || !isHexSHA256(f[1]) {
			return nil, fmt.Errorf("seals.log line %d malformed", i+1)
		}
		out = append(out, sealEntry{RunID: f[0], Hash: f[1]})
	}
	return out, nil
}

func isHexSHA256(s string) bool {
	if len(s) != 64 {
		return false
	}
	for _, c := range s {
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return false
		}
	}
	return true
}

func short(h string) string {
	if len(h) > 12 {
		return h[:12]
	}
	return h
}
