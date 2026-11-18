package evidencestore

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sort"
	"strings"
	"time"

	"github.com/maximhq/bifrost/bench/utils"
)

// ListingFormat names the mirror listing's format.
const ListingFormat = "heimdall-evidence-mirror/1"

// maxListingBytes bounds how much of a listing object is read.
const maxListingBytes = 256 << 20

// Listing is the mirror's record of one experiment, stored at
// <org_id>/<exp_id>/.mirror.json and written after every file it names. It
// is the commit point of a mirror pass: a file is in the mirror when the
// listing names it, and restore reads only what the listing names.
type Listing struct {
	Format       string      `json:"format"`
	OrgID        string      `json:"org_id"`
	ExpID        string      `json:"exp_id"`
	ManifestHash string      `json:"manifest_hash"`
	Seals        int         `json:"seals"`               // lines of the mirrored seals.log
	LastSeal     string      `json:"last_seal,omitempty"` // its last seal hash
	Generation   int         `json:"generation"`          // +1 each time the listing changes
	UpdatedAt    time.Time   `json:"updated_at"`
	Files        []FileEntry `json:"files"` // sorted by path
}

// FileEntry is one mirrored file: its path inside the experiment folder, its
// size and its sha256.
type FileEntry struct {
	Path   string `json:"path"`
	Size   int64  `json:"size"`
	SHA256 string `json:"sha256"`
}

func (l *Listing) file(p string) (FileEntry, bool) {
	i := sort.Search(len(l.Files), func(i int) bool { return l.Files[i].Path >= p })
	if i < len(l.Files) && l.Files[i].Path == p {
		return l.Files[i], true
	}
	return FileEntry{}, false
}

// expPrefix is the key prefix of an experiment in the mirror.
func expPrefix(orgID, expID string) string { return orgID + "/" + expID + "/" }

func listingKey(orgID, expID string) string { return expPrefix(orgID, expID) + listingName }

// validate checks a listing read from the mirror before anything trusts it.
func (l *Listing) validate(orgID, expID string) error {
	if l.Format != ListingFormat {
		return fmt.Errorf("listing format %q, want %q", l.Format, ListingFormat)
	}
	if l.OrgID != orgID || l.ExpID != expID {
		return fmt.Errorf("listing names %s/%s, want %s/%s", l.OrgID, l.ExpID, orgID, expID)
	}
	for i, f := range l.Files {
		if err := mirrorPathAllowed(f.Path); err != nil {
			return fmt.Errorf("listing entry %d: %w", i, err)
		}
		if i > 0 && l.Files[i-1].Path >= f.Path {
			return fmt.Errorf("listing entries not sorted or repeated at %s", f.Path)
		}
		if !isHexSHA256(f.SHA256) || f.Size < 0 {
			return fmt.Errorf("listing entry %s: bad size or sha256", f.Path)
		}
	}
	return nil
}

// validRelPath accepts a path inside an experiment folder: a valid key that
// is not the listing itself and has no segment a folder mirror reserves for
// its temporary files. Dot files are allowed (a sealed run may hold one).
func validRelPath(p string) error {
	if err := ValidKey(p); err != nil {
		return err
	}
	if p == listingName {
		return fmt.Errorf("path %q is reserved for the listing", p)
	}
	for _, seg := range strings.Split(p, "/") {
		if strings.HasPrefix(seg, tmpPrefix) {
			return fmt.Errorf("path %q: reserved segment", p)
		}
	}
	return nil
}

// readListing returns the experiment's listing and the sha256 of its bytes,
// or ErrNotFound.
func readListing(ctx context.Context, b Blob, orgID, expID string) (*Listing, string, error) {
	rc, _, err := b.Get(ctx, listingKey(orgID, expID))
	if err != nil {
		return nil, "", err
	}
	defer rc.Close()
	data, err := io.ReadAll(io.LimitReader(rc, maxListingBytes+1))
	if err != nil {
		return nil, "", fmt.Errorf("read listing %s/%s: %w", orgID, expID, err)
	}
	if len(data) > maxListingBytes {
		return nil, "", fmt.Errorf("listing %s/%s is larger than %d bytes", orgID, expID, maxListingBytes)
	}
	var l Listing
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&l); err != nil {
		return nil, "", fmt.Errorf("decode listing %s/%s: %w", orgID, expID, err)
	}
	if err := l.validate(orgID, expID); err != nil {
		return nil, "", fmt.Errorf("listing %s/%s: %w", orgID, expID, err)
	}
	return &l, utils.SHA256Bytes(data), nil
}

// encodeListing returns the listing's bytes: indented JSON, readable by an
// auditor with no tool but a text viewer.
func encodeListing(l *Listing) ([]byte, error) {
	b, err := json.MarshalIndent(l, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(b, '\n'), nil
}

// isNotFound reports a missing object.
func isNotFound(err error) bool { return errors.Is(err, ErrNotFound) }

// MirroredExperiments returns the ids of the organization's experiments that
// have a listing in the mirror, sorted.
func MirroredExperiments(ctx context.Context, b Blob, orgID string) ([]string, error) {
	if err := validID("org_id", orgID); err != nil {
		return nil, err
	}
	objs, err := b.List(ctx, orgID+"/")
	if err != nil {
		return nil, err
	}
	var out []string
	for _, o := range objs {
		parts := strings.Split(o.Key, "/")
		if len(parts) == 3 && parts[2] == listingName && validID("exp_id", parts[1]) == nil {
			out = append(out, parts[1])
		}
	}
	sort.Strings(out)
	return out, nil
}

// MirroredOrgs returns the ids of the organizations that have at least one
// experiment listing in the mirror, sorted. It lists the whole mirror, which
// is what a restore after a disk loss needs once.
func MirroredOrgs(ctx context.Context, b Blob) ([]string, error) {
	objs, err := b.List(ctx, "")
	if err != nil {
		return nil, err
	}
	seen := map[string]bool{}
	for _, o := range objs {
		parts := strings.Split(o.Key, "/")
		if len(parts) == 3 && parts[2] == listingName && validID("org_id", parts[0]) == nil && validID("exp_id", parts[1]) == nil {
			seen[parts[0]] = true
		}
	}
	out := make([]string, 0, len(seen))
	for id := range seen {
		out = append(out, id)
	}
	sort.Strings(out)
	return out, nil
}
