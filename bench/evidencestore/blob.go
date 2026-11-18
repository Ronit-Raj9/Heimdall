// Package evidencestore keeps a second copy of the sealed evidence: it mirrors
// sealed experiment folders to object storage (a local directory or an
// S3-compatible bucket), restores them with every seal and the seals.log
// chain checked before anything moves into place, compares a mirror with the
// local folders, archives old experiments under a per-organization retention
// setting, and backs up the database (docs/evidence-storage.md).
//
// The run folder stays the evidence (docs/platform-design.md, rule 2). The
// mirror is a byte-for-byte copy of the immutable files of an experiment plus
// one listing of their sha256s; nothing here changes a sealed folder.
package evidencestore

import (
	"context"
	"errors"
	"fmt"
	"io"
	"path"
	"strings"
	"time"
	"unicode/utf8"
)

// ErrNotFound is returned by Blob.Get and Blob.Head for a missing key.
var ErrNotFound = errors.New("evidencestore: object not found")

// ErrChecksum is returned when stored or received bytes do not hash to the
// sha256 they were sent or recorded with.
var ErrChecksum = errors.New("evidencestore: checksum mismatch")

// ObjectInfo describes one stored object.
type ObjectInfo struct {
	Key  string
	Size int64
	// SHA256 is the lowercase hex sha256 the backend recorded for the object
	// when it was put, or "" when the backend does not know it (List results
	// on S3, objects written by something else). It is a claim, not a proof:
	// readers that need proof hash the bytes they receive.
	SHA256       string
	ETag         string
	LastModified time.Time
}

// PutInput is one object to store. Body is read with ReadAt, so a retry or a
// multipart part can read any range again; *os.File and *bytes.Reader both
// fit.
type PutInput struct {
	Body io.ReaderAt
	Size int64
	// SHA256 is the lowercase hex sha256 of the whole body. It is required:
	// the backend refuses the write with ErrChecksum when the bytes it
	// receives or stores hash to something else.
	SHA256      string
	ContentType string
}

// Blob is object storage: a flat namespace of slash-separated keys. Keys are
// checked with ValidKey before any backend sees them.
type Blob interface {
	// Put stores the object, replacing any object at key. It returns only
	// after the backend has the whole object and has checked its checksum.
	Put(ctx context.Context, key string, in PutInput) error
	// Get opens the object. The caller closes the reader. The returned
	// info's SHA256 is the backend's claim; verify with VerifyingReader.
	Get(ctx context.Context, key string) (io.ReadCloser, ObjectInfo, error)
	// Head returns the object's size and recorded sha256 without its body.
	Head(ctx context.Context, key string) (ObjectInfo, error)
	// List returns every object whose key starts with prefix, sorted by key.
	List(ctx context.Context, prefix string) ([]ObjectInfo, error)
	// Delete removes the object. A missing key is not an error.
	Delete(ctx context.Context, key string) error
	// String names the backend and location for logs and reports, with no
	// credentials: "file:///srv/mirror" or "s3://bucket/prefix".
	String() string
}

// ValidKey accepts a clean relative slash path: no leading or trailing
// slash, no empty, "." or ".." segment, no backslash, no control character,
// valid UTF-8, at most 1024 bytes. Every key the package builds passes; the
// check stops a crafted name from leaving a local mirror's folder.
func ValidKey(key string) error {
	if key == "" || len(key) > 1024 || !utf8.ValidString(key) {
		return fmt.Errorf("invalid object key %q", key)
	}
	if strings.ContainsRune(key, '\\') || path.Clean(key) != key || strings.HasPrefix(key, "/") {
		return fmt.Errorf("invalid object key %q", key)
	}
	for _, r := range key {
		if r < 0x20 || r == 0x7f {
			return fmt.Errorf("invalid object key %q: control character", key)
		}
	}
	for _, seg := range strings.Split(key, "/") {
		if seg == "" || seg == "." || seg == ".." {
			return fmt.Errorf("invalid object key %q", key)
		}
	}
	return nil
}

// ValidPrefix accepts "" or a prefix that is a valid key, optionally ending
// in one slash.
func ValidPrefix(prefix string) error {
	if prefix == "" {
		return nil
	}
	return ValidKey(strings.TrimSuffix(prefix, "/"))
}
