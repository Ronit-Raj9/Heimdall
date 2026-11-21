package projectfs

import (
	"context"
	"os"
	"time"
)

// MaxLargeFileBytes is the most any area accepts through WriteLarge. It
// matches lib.MaxUploadBytes (the upload route body limit).
const MaxLargeFileBytes = 64 << 20

// LargeFileCaps is the per-area cap for WriteLarge. An area missing here
// gets MaxFileBytes. Operators may lower an entry from an init() hook.
var LargeFileCaps = map[Area]int64{
	AreaAgents:      MaxLargeFileBytes,
	AreaBenchmarks:  MaxLargeFileBytes,
	AreaExperiments: MaxLargeFileBytes,
	AreaPolicies:    MaxLargeFileBytes,
	AreaUploads:     MaxLargeFileBytes,
}

// LargeFileCap returns the WriteLarge cap of an area, never above
// MaxLargeFileBytes.
func LargeFileCap(a Area) int64 {
	c, ok := LargeFileCaps[a]
	if !ok || c <= 0 {
		return MaxFileBytes
	}
	if c > MaxLargeFileBytes {
		return MaxLargeFileBytes
	}
	return c
}

// LargeWriter is implemented by stores that accept files over MaxFileBytes
// (up to LargeFileCap of the area). Open's store implements it.
type LargeWriter interface {
	WriteLarge(ctx context.Context, a Area, rel string, data []byte, o WriteOpts) (Entry, error)
}

// WriteLarge is Write with the area's large-file cap. Files up to
// MaxFileBytes are versioned like Write; bigger ones are written atomically
// but not copied into the version store (it would double their disk use).
func (s *store) WriteLarge(ctx context.Context, a Area, rel string, data []byte, o WriteOpts) (Entry, error) {
	if int64(len(data)) > LargeFileCap(a) {
		return Entry{}, ErrTooLarge
	}
	if len(data) <= MaxFileBytes {
		return s.Write(ctx, a, rel, data, o)
	}
	full, err := s.Resolve(a, rel)
	if err != nil {
		return Entry{}, err
	}
	c, _ := cleanRel(rel)
	if c == "" || c == "." {
		return Entry{}, ErrInvalid
	}
	if st, err := os.Stat(full); err == nil && st.IsDir() {
		return Entry{}, ErrInvalid
	}
	if err := validate(a, c, data); err != nil {
		return Entry{}, err
	}
	unlock := s.lock()
	defer unlock()
	if err := checkMatch(full, o.IfMatch); err != nil {
		return Entry{}, err
	}
	if err := writeAtomic(full, data); err != nil {
		return Entry{}, err
	}
	return Entry{Path: c, Size: int64(len(data)), ModTime: time.Now().UTC(), SHA256: sum(data)}, nil
}
