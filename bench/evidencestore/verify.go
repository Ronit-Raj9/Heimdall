package evidencestore

import (
	"context"
	"errors"
	"fmt"
	"io"
	"sort"
	"strings"
	"sync"

	"github.com/maximhq/bifrost/bench/utils"
)

// VerifyReport compares the mirror copy of one experiment with the local
// folder.
type VerifyReport struct {
	OrgID         string `json:"org_id"`
	ExpID         string `json:"exp_id"`
	Mirror        string `json:"mirror"`
	Generation    int    `json:"generation"`
	ListingSHA256 string `json:"listing_sha256"`
	Archived      bool   `json:"archived"` // local folder is an archive stub; only the mirror side is checked
	Deep          bool   `json:"deep"`     // every object was downloaded and hashed
	// Missing: immutable local files the mirror does not hold (not mirrored
	// yet, or listed but the object is gone).
	Missing []string `json:"missing,omitempty"`
	// Extra: files the mirror holds that are not in the local folder.
	Extra []string `json:"extra,omitempty"`
	// Mismatched: files on both sides whose size or sha256 differ, and
	// objects whose stored size, recorded sha256 or (deep) bytes differ from
	// the listing.
	Mismatched []string `json:"mismatched,omitempty"`
	// Unlisted: objects under the experiment's prefix that no listing names,
	// left by an interrupted pass. Harmless; reported for completeness.
	Unlisted []string `json:"unlisted,omitempty"`
	// Local: the local folder failed its own seal or chain check (or its
	// stub does not match the mirror). The local copy is the suspect one.
	Local   string   `json:"local_error,omitempty"`
	Pending []string `json:"pending,omitempty"` // local run folders not sealed yet; not expected in the mirror
	Skipped []string `json:"skipped,omitempty"` // local entries never mirrored (dot names, non-regular files)

	listing *Listing
}

// OK reports a mirror that matches the local folder: nothing missing or
// mismatched and the local folder verifies. Extra files are allowed: the
// mirror keeps evidence the local folder lost, and they are listed.
func (r *VerifyReport) OK() bool {
	return len(r.Missing) == 0 && len(r.Mismatched) == 0 && r.Local == ""
}

// Summary is a one-line account of the problems.
func (r *VerifyReport) Summary() string {
	var parts []string
	add := func(name string, items []string) {
		if len(items) > 0 {
			parts = append(parts, fmt.Sprintf("%d %s (first: %s)", len(items), name, items[0]))
		}
	}
	add("missing", r.Missing)
	add("mismatched", r.Mismatched)
	add("extra", r.Extra)
	if r.Local != "" {
		parts = append(parts, "local: "+r.Local)
	}
	if len(parts) == 0 {
		return "ok"
	}
	return strings.Join(parts, "; ")
}

// Verify compares the mirror with the local folder (see VerifyReport). With
// deep, every listed object is downloaded and hashed; otherwise the
// object's stored size and recorded sha256 are compared with the listing. A
// mirror with no listing for the experiment returns ErrNotFound.
func (m *Mirror) Verify(ctx context.Context, org Org, expID string, deep bool) (*VerifyReport, error) {
	unlock, err := m.lockExp(org, expID)
	if err != nil {
		return nil, err
	}
	defer unlock()
	return m.verifyLocked(ctx, org, expID, deep)
}

func (m *Mirror) verifyLocked(ctx context.Context, org Org, expID string, deep bool) (*VerifyReport, error) {
	listing, sum, err := readListing(ctx, m.Blob, org.ID, expID)
	if err != nil {
		return nil, fmt.Errorf("verify %s/%s: %w", org.ID, expID, err)
	}
	rep := &VerifyReport{OrgID: org.ID, ExpID: expID, Mirror: m.Blob.String(), Generation: listing.Generation,
		ListingSHA256: sum, Deep: deep, listing: listing}
	// Mirror side: every listed object present, same size (an append-only
	// log may be longer: a later pass sent it before failing), same sha256.
	objects, err := m.Blob.List(ctx, expPrefix(org.ID, expID))
	if err != nil {
		return nil, fmt.Errorf("verify %s/%s: list mirror: %w", org.ID, expID, err)
	}
	stored := make(map[string]ObjectInfo, len(objects))
	for _, o := range objects {
		stored[strings.TrimPrefix(o.Key, expPrefix(org.ID, expID))] = o
	}
	var checks []FileEntry
	for _, e := range listing.Files {
		o, ok := stored[e.Path]
		delete(stored, e.Path)
		switch {
		case !ok:
			// Gone from the mirror: missing whatever the local side holds,
			// since a restore would fail on it.
			rep.Missing = append(rep.Missing, e.Path+": listed but the object is gone")
		case o.Size != e.Size && !(isAppendOnlyLog(e.Path) && o.Size > e.Size):
			rep.Mismatched = append(rep.Mismatched, fmt.Sprintf("%s: mirror object is %d bytes, listing says %d", e.Path, o.Size, e.Size))
		default:
			checks = append(checks, e)
		}
	}
	delete(stored, listingName)
	for p := range stored {
		rep.Unlisted = append(rep.Unlisted, p)
	}
	bad, err := m.checkObjects(ctx, org.ID, expID, checks, deep)
	if err != nil {
		return nil, fmt.Errorf("verify %s/%s: %w", org.ID, expID, err)
	}
	rep.Mismatched = append(rep.Mismatched, bad...)
	// Local side.
	dir := org.expDir(expID)
	if !isLive(dir) {
		if stub, err := ReadStub(org.root(), expID); err == nil {
			rep.Archived = true
			if err := stub.matches(listing, sum); err != nil {
				rep.Local = err.Error()
			}
		} else {
			rep.Local = "no local folder for the experiment (bring it back with evidence restore)"
			for _, e := range listing.Files {
				rep.Extra = append(rep.Extra, e.Path)
			}
		}
		sortReport(rep)
		return rep, nil
	}
	snap, err := takeSnapshot(ctx, org, expID)
	if err != nil {
		if errors.Is(err, ErrRefused) {
			rep.Local = err.Error()
			sortReport(rep)
			return rep, nil
		}
		return nil, err
	}
	rep.Pending = snap.Pending
	rep.Skipped = snap.Skipped
	for _, f := range snap.Files {
		e, ok := listing.file(f.Path)
		switch {
		case !ok:
			rep.Missing = append(rep.Missing, f.Path)
		case isAppendOnlyLog(f.Path):
			// An append-only log may have grown since the last pass; it
			// must still start with the mirrored bytes.
			if f.Size < e.Size || hashPrefix(f.data, e.Size) != e.SHA256 {
				rep.Mismatched = append(rep.Mismatched, f.Path+": local log does not extend the mirrored one")
			} else if f.Size > e.Size {
				rep.Missing = append(rep.Missing, f.Path+": new lines not mirrored yet")
			}
		case e.SHA256 != f.SHA256 || e.Size != f.Size:
			rep.Mismatched = append(rep.Mismatched, fmt.Sprintf("%s: local sha256 %s, mirror %s", f.Path, short(f.SHA256), short(e.SHA256)))
		}
	}
	for _, e := range listing.Files {
		if _, ok := snap.file(e.Path); !ok {
			rep.Extra = append(rep.Extra, e.Path)
		}
	}
	sortReport(rep)
	return rep, nil
}

// checkObjects compares objects with their listing entries: the recorded
// sha256 (Head), or with deep the bytes themselves.
func (m *Mirror) checkObjects(ctx context.Context, orgID, expID string, files []FileEntry, deep bool) ([]string, error) {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	jobs := make(chan FileEntry)
	var mu sync.Mutex
	var bad []string
	var firstErr error
	var wg sync.WaitGroup
	for range min(m.workers(), max(1, len(files))) {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for e := range jobs {
				problem, err := m.checkObject(ctx, expPrefix(orgID, expID)+e.Path, e, deep)
				mu.Lock()
				if err != nil && firstErr == nil {
					firstErr = fmt.Errorf("check %s: %w", e.Path, err)
					cancel()
				}
				if problem != "" {
					bad = append(bad, e.Path+": "+problem)
				}
				mu.Unlock()
			}
		}()
	}
	for _, e := range files {
		select {
		case jobs <- e:
		case <-ctx.Done():
		}
	}
	close(jobs)
	wg.Wait()
	if firstErr != nil {
		return nil, firstErr
	}
	return bad, ctx.Err()
}

func (m *Mirror) checkObject(ctx context.Context, key string, e FileEntry, deep bool) (string, error) {
	if !deep {
		info, err := m.Blob.Head(ctx, key)
		if err != nil {
			if isNotFound(err) {
				return "object is gone", nil
			}
			return "", err
		}
		if info.Size > e.Size && isAppendOnlyLog(e.Path) {
			return "", nil // a longer log carries its own newer claim; -deep checks its prefix
		}
		if info.SHA256 != "" && info.SHA256 != e.SHA256 {
			return fmt.Sprintf("object records sha256 %s, listing says %s", short(info.SHA256), short(e.SHA256)), nil
		}
		return "", nil
	}
	rc, _, err := m.Blob.Get(ctx, key)
	if err != nil {
		if isNotFound(err) {
			return "object is gone", nil
		}
		return "", err
	}
	defer rc.Close()
	var body io.Reader = rc
	if isAppendOnlyLog(e.Path) {
		body = io.LimitReader(rc, e.Size)
	}
	if _, err := io.Copy(io.Discard, VerifyingReader(body, e.Size, e.SHA256)); err != nil {
		if errors.Is(err, ErrChecksum) {
			return err.Error(), nil
		}
		return "", err
	}
	return "", nil
}

func hashPrefix(b []byte, n int64) string {
	if int64(len(b)) < n {
		return ""
	}
	return utils.SHA256Bytes(b[:n])
}

func sortReport(r *VerifyReport) {
	for _, s := range [][]string{r.Missing, r.Extra, r.Mismatched, r.Unlisted} {
		sort.Strings(s)
	}
}
