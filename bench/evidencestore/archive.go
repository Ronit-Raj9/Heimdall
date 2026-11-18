package evidencestore

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/maximhq/bifrost/bench/utils"
)

// stubFile is the one file left in an archived experiment's folder.
const stubFile = "archived.json"

// StubFormat names the archive stub's format.
const StubFormat = "heimdall-archive-stub/1"

// Stub is what stays on local disk when an experiment is archived: enough
// for the UI to list it and offer a restore, and an anchor for the restore.
// The folder <root>/<exp_id>/ holds only archived.json, so the store does not
// list the experiment (no manifest.json), the id can not be reused (the
// folder exists), and Restore can check the mirror against the hashes
// recorded here before it brings anything back.
type Stub struct {
	Format       string    `json:"format"`
	OrgID        string    `json:"org_id"`
	ExpID        string    `json:"exp_id"`
	Name         string    `json:"name,omitempty"`
	Suite        string    `json:"suite,omitempty"`
	Arms         []string  `json:"arms,omitempty"`
	CreatedAt    time.Time `json:"created_at"`
	LastActivity time.Time `json:"last_activity"`
	ArchivedAt   time.Time `json:"archived_at"`
	ManifestHash string    `json:"manifest_hash"`
	Runs         int       `json:"runs"` // sealed runs
	Files        int       `json:"files"`
	Bytes        int64     `json:"bytes"`
	// The mirror copy this stub stands for.
	Mirror        string `json:"mirror"`
	ListingKey    string `json:"listing_key"`
	ListingSHA256 string `json:"listing_sha256"`
	Generation    int    `json:"generation"`
	LastSeal      string `json:"last_seal,omitempty"`
}

// matches checks the mirror's current listing against the stub.
func (s *Stub) matches(l *Listing, listingSHA string) error {
	if s.ListingSHA256 != listingSHA {
		return fmt.Errorf("mirror listing hashes to %s, the archive stub recorded %s: the mirror changed after archiving", short(listingSHA), short(s.ListingSHA256))
	}
	if s.Runs != l.Seals || s.LastSeal != l.LastSeal || s.ManifestHash != l.ManifestHash {
		return fmt.Errorf("mirror listing (%d seals, last %s) does not match the archive stub (%d seals, last %s)", l.Seals, short(l.LastSeal), s.Runs, short(s.LastSeal))
	}
	return nil
}

func readStubFile(p string) (*Stub, error) {
	b, err := os.ReadFile(p)
	if err != nil {
		return nil, err
	}
	var s Stub
	if err := json.Unmarshal(b, &s); err != nil {
		return nil, fmt.Errorf("decode %s: %w", p, err)
	}
	if s.Format != StubFormat {
		return nil, fmt.Errorf("%s: format %q, want %q", p, s.Format, StubFormat)
	}
	return &s, nil
}

// ReadStub returns the archive stub of an experiment in a store root, or an
// error wrapping fs.ErrNotExist when the experiment is not archived.
func ReadStub(root, expID string) (*Stub, error) {
	if err := validID("exp_id", expID); err != nil {
		return nil, err
	}
	return readStubFile(filepath.Join(root, expID, stubFile))
}

// ListArchived returns the archive stubs under a store root, sorted by
// experiment id. The UI lists these beside the live experiments and offers
// a restore for each.
func ListArchived(root string) ([]Stub, error) {
	entries, err := os.ReadDir(root)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	var out []Stub
	for _, e := range entries {
		if !e.IsDir() || validID("exp_id", e.Name()) != nil {
			continue
		}
		s, err := readStubFile(filepath.Join(root, e.Name(), stubFile))
		if err != nil {
			continue
		}
		out = append(out, *s)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ExpID < out[j].ExpID })
	return out, nil
}

// liveLeaseWindow is how recent a lease heartbeat must be for Archive to
// treat its slot as running. A lease left by a crash stops counting after
// it.
const liveLeaseWindow = time.Hour

// Archive moves one experiment to the mirror and frees its local space.
// Nothing is deleted unless the mirror copy has been restored and verified:
//
//  1. refuse while a slot holds a live lease (heartbeat within the last
//     hour), or while any run folder is unsealed;
//  2. a mirror pass, with every seal, chain and immutability check;
//  3. refuse when an entry would not be mirrored (Skipped) or the mirror is
//     missing or differs from any local file (Verify);
//  4. a restore drill: the whole listing is downloaded into a staging
//     folder and checked exactly as Restore checks it, then discarded;
//  5. the stub is written in a staging folder, the experiment folder is
//     renamed into the trash, and the trashed folder is compared with the
//     listing once more: a file written after step 2 (an analysis, a
//     review, a new seal) puts the folder back and refuses;
//  6. the stub folder is renamed into place and the trash deleted.
//
// A crash at any point is finished or undone by the next archive, restore
// or sweep (recoverExp).
func (m *Mirror) Archive(ctx context.Context, org Org, expID string) (*Stub, error) {
	unlock, err := m.lockExp(org, expID)
	if err != nil {
		return nil, err
	}
	defer unlock()
	if err := m.recoverExp(org, expID); err != nil {
		return nil, err
	}
	refuse := func(format string, args ...any) error {
		return fmt.Errorf("archive %s/%s: %s: %w", org.ID, expID, fmt.Sprintf(format, args...), ErrRefused)
	}
	dir := org.expDir(expID)
	if slot, ok := liveLease(dir, m.now()); ok {
		return nil, refuse("slot %s holds a live lease; the experiment is running", slot)
	}
	res, err := m.experimentLocked(ctx, org, expID, MirrorOptions{Repair: true})
	if err != nil {
		return nil, fmt.Errorf("archive %s/%s: mirror pass: %w", org.ID, expID, err)
	}
	if len(res.Pending) > 0 {
		return nil, refuse("%d run folder(s) are not sealed (%s); resume or finish the experiment first", len(res.Pending), res.Pending[0])
	}
	if len(res.Skipped) > 0 {
		return nil, refuse("%d entr(ies) would not be mirrored (%s); move them out of the experiment folder first", len(res.Skipped), res.Skipped[0])
	}
	rep, err := m.verifyLocked(ctx, org, expID, false)
	if err != nil {
		return nil, fmt.Errorf("archive %s/%s: verify mirror: %w", org.ID, expID, err)
	}
	if !rep.OK() {
		return nil, refuse("mirror copy does not verify (%s); nothing deleted", rep.Summary())
	}
	drill, err := m.stage(ctx, org, expID, org.root(), nil)
	if drill != nil {
		removeTree(drill.dir)
	}
	if err != nil {
		return nil, refuse("restore drill from the mirror failed (%v); nothing deleted", err)
	}
	if drill.listingSHA != rep.ListingSHA256 {
		return nil, refuse("mirror listing changed during the archive")
	}
	listing := drill.listing
	m0, err := readManifest(dir)
	if err != nil {
		return nil, err
	}
	last, err := lastActivity(dir, m0.CreatedAt)
	if err != nil {
		return nil, err
	}
	var bytes int64
	for _, f := range listing.Files {
		bytes += f.Size
	}
	stub := &Stub{
		Format: StubFormat, OrgID: org.ID, ExpID: expID, Name: m0.Experiment.Name, Suite: m0.Experiment.Suite,
		Arms: sortedKeys(m0.Arms), CreatedAt: m0.CreatedAt, LastActivity: last, ArchivedAt: m.now().UTC(),
		ManifestHash: listing.ManifestHash, Runs: listing.Seals, Files: len(listing.Files), Bytes: bytes,
		Mirror: m.Blob.String(), ListingKey: listingKey(org.ID, expID), ListingSHA256: drill.listingSHA,
		Generation: listing.Generation, LastSeal: listing.LastSeal,
	}
	b, err := json.MarshalIndent(stub, "", "  ")
	if err != nil {
		return nil, err
	}
	stubDir, err := os.MkdirTemp(stateSubdir(org.root(), "staging"), expID+".")
	if err != nil {
		return nil, err
	}
	defer removeTree(stubDir)
	if err := writeFileSync(filepath.Join(stubDir, stubFile), append(b, '\n')); err != nil {
		return nil, fmt.Errorf("archive %s/%s: write stub: %w", org.ID, expID, err)
	}
	trashed, err := trashPath(org.root(), expID)
	if err != nil {
		return nil, err
	}
	if err := os.Rename(dir, trashed); err != nil {
		return nil, fmt.Errorf("archive %s/%s: move folder aside: %w", org.ID, expID, err)
	}
	if diff := diffAgainstListing(trashed, listing); diff != "" {
		if rerr := os.Rename(trashed, dir); rerr != nil {
			return nil, fmt.Errorf("archive %s/%s: %s, and putting the folder back from %s failed: %w", org.ID, expID, diff, trashed, rerr)
		}
		return nil, refuse("%s after the mirror pass; folder put back, try again", diff)
	}
	if err := os.Rename(stubDir, dir); err != nil {
		// Something recreated the folder meanwhile. The trashed copy is
		// complete and verified; keep it for a manual check.
		return nil, fmt.Errorf("archive %s/%s: put the stub in place: %w; the experiment folder is at %s", org.ID, expID, err, trashed)
	}
	if err := syncDir(org.root()); err != nil {
		return nil, err
	}
	removeTree(trashed)
	m.logger().Info("experiment archived", "org_id", org.ID, "exp_id", expID, "bytes", bytes, "mirror", m.Blob.String())
	return stub, nil
}

// liveLease returns a slot whose lease.json heartbeat is recent.
func liveLease(dir string, now time.Time) (string, bool) {
	matches, _ := filepath.Glob(filepath.Join(dir, slotsDir, "*", "lease.json"))
	for _, p := range matches {
		var l schemas.Lease
		b, err := os.ReadFile(p)
		if err != nil || json.Unmarshal(b, &l) != nil || now.Sub(l.HeartbeatAt) < liveLeaseWindow {
			// An unreadable lease counts as live: better to wait than to
			// archive under a running slot.
			return filepath.Base(filepath.Dir(p)), true
		}
	}
	return "", false
}

// diffAgainstListing compares a trashed experiment folder with the listing
// and names the first difference: a file the listing does not name (other
// than lease files), or one whose size or sha256 differs. Files the listing
// names that are absent locally are fine: the mirror holds them.
func diffAgainstListing(dir string, l *Listing) string {
	var diff string
	_ = filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			diff = err.Error()
			return fs.SkipAll
		}
		if d.IsDir() {
			return nil
		}
		rel, _ := filepath.Rel(dir, p)
		rel = filepath.ToSlash(rel)
		parts := strings.Split(rel, "/")
		if len(parts) == 3 && parts[0] == slotsDir && (parts[2] == "lease.json" || parts[2] == "lease.lock") {
			return nil
		}
		e, ok := l.file(rel)
		if !ok {
			diff = rel + " is not in the mirror"
			return fs.SkipAll
		}
		info, err := d.Info()
		if err != nil || info.Size() != e.Size {
			diff = rel + " changed"
			return fs.SkipAll
		}
		if sum, err := utils.SHA256File(p); err != nil || sum != e.SHA256 {
			diff = rel + " changed"
			return fs.SkipAll
		}
		return nil
	})
	return diff
}

// writeFileSync writes a new file and syncs it and its folder.
func writeFileSync(p string, b []byte) error {
	f, err := os.OpenFile(p, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if err != nil {
		return err
	}
	_, err = f.Write(b)
	if err == nil {
		err = f.Sync()
	}
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return err
	}
	return syncDir(filepath.Dir(p))
}

// RetentionItem is the outcome of one experiment in a retention pass.
type RetentionItem struct {
	OrgID        string    `json:"org_id"`
	ExpID        string    `json:"exp_id"`
	LastActivity time.Time `json:"last_activity"`
	Action       string    `json:"action"` // archived, would_archive, kept, failed
	Reason       string    `json:"reason,omitempty"`
	Bytes        int64     `json:"bytes,omitempty"`
}

// RetentionOptions controls ApplyRetention.
type RetentionOptions struct {
	Days   int  // archive experiments idle for more than this many days; 0 archives nothing
	DryRun bool // report what would be archived, change nothing
}

// ApplyRetention archives every experiment of the organization whose last
// activity (manifest creation, a seal, a review, an analysis, a regrade, a
// lease) is older than opts.Days. A failure on one experiment is reported
// and does not stop the others.
func (m *Mirror) ApplyRetention(ctx context.Context, org Org, opts RetentionOptions) ([]RetentionItem, error) {
	if opts.Days <= 0 {
		return nil, nil
	}
	if err := m.recover(org); err != nil {
		m.logger().Warn("evidence: interrupted archive or restore not recovered", "org_id", org.ID, "error", err.Error())
	}
	ids, err := org.Store.ListExperiments(ctx)
	if err != nil {
		return nil, err
	}
	cutoff := m.now().Add(-time.Duration(opts.Days) * 24 * time.Hour)
	var out []RetentionItem
	failed := 0
	for _, id := range ids {
		if err := ctx.Err(); err != nil {
			return out, err
		}
		dir := org.expDir(id)
		man, err := readManifest(dir)
		if err != nil {
			out = append(out, RetentionItem{OrgID: org.ID, ExpID: id, Action: "failed", Reason: err.Error()})
			failed++
			continue
		}
		last, err := lastActivity(dir, man.CreatedAt)
		if err != nil {
			out = append(out, RetentionItem{OrgID: org.ID, ExpID: id, Action: "failed", Reason: err.Error()})
			failed++
			continue
		}
		item := RetentionItem{OrgID: org.ID, ExpID: id, LastActivity: last}
		switch {
		case !last.Before(cutoff):
			item.Action = "kept"
		case opts.DryRun:
			item.Action = "would_archive"
		default:
			stub, err := m.Archive(ctx, org, id)
			if err != nil {
				item.Action, item.Reason = "failed", err.Error()
				failed++
			} else {
				item.Action, item.Bytes = "archived", stub.Bytes
			}
		}
		out = append(out, item)
	}
	if failed > 0 {
		return out, fmt.Errorf("retention %s: %d experiment(s) not archived", org.ID, failed)
	}
	return out, nil
}

// lastActivity is the newest of the manifest's creation time and the
// modification times of the files and folders that change when the
// experiment is used: seals.log, reviews.log, every folder under analysis/,
// regrades/ and reviews/, every slot's runs/ folder and lease.json.
func lastActivity(dir string, created time.Time) (time.Time, error) {
	last := created
	bump := func(p string) error {
		st, err := os.Stat(p)
		if err != nil {
			if errors.Is(err, fs.ErrNotExist) {
				return nil
			}
			return err
		}
		if st.ModTime().After(last) {
			last = st.ModTime()
		}
		return nil
	}
	for _, p := range []string{sealsLogFile, reviewsLogFile, "analysis", "regrades", "reviews", slotsDir} {
		if err := bump(filepath.Join(dir, p)); err != nil {
			return time.Time{}, err
		}
	}
	for _, pat := range []string{"analysis/*", "regrades/*", "reviews/*", "slots/*/runs", "slots/*/lease.json"} {
		matches, err := filepath.Glob(filepath.Join(dir, filepath.FromSlash(pat)))
		if err != nil {
			return time.Time{}, err
		}
		for _, p := range matches {
			if err := bump(p); err != nil {
				return time.Time{}, err
			}
		}
	}
	return last.UTC(), nil
}

func readManifest(dir string) (*schemas.Manifest, error) {
	b, err := os.ReadFile(filepath.Join(dir, manifestFile))
	if err != nil {
		return nil, err
	}
	var m schemas.Manifest
	if err := json.Unmarshal(b, &m); err != nil {
		return nil, fmt.Errorf("decode manifest.json: %w", err)
	}
	return &m, nil
}

func sortedKeys[V any](m map[string]V) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// RetentionSettingKey is the per-organization setting (settings table,
// docs/platform-design.md) that holds the number of idle days after which
// experiments are archived. Absent or 0 means never.
const RetentionSettingKey = "retention.archive_after_days"

// SettingsReader reads one organization setting; *db.DB fits.
type SettingsReader interface {
	GetSetting(ctx context.Context, orgID, key string) (value string, found bool, err error)
}

// RetentionDays reads an organization's retention setting: 0 when absent.
func RetentionDays(ctx context.Context, s SettingsReader, orgID string) (int, error) {
	v, found, err := s.GetSetting(ctx, orgID, RetentionSettingKey)
	if err != nil || !found {
		return 0, err
	}
	days, err := strconv.Atoi(strings.TrimSpace(v))
	if err != nil || days < 0 {
		return 0, fmt.Errorf("setting %s of org %s is %q: want a whole number of days", RetentionSettingKey, orgID, v)
	}
	return days, nil
}
