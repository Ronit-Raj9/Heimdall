package traceinbox

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"sync"
	"time"

	"github.com/maximhq/bifrost/bench/traceimport"
)

// Store keeps the inbox. FileStore is the only implementation here; the
// hosted build can index the same items behind this interface.
type Store interface {
	// Items returns every item, in first-seen order.
	Items() ([]Item, error)
	// Update runs fn over every item under the store lock and saves the
	// items it returns. fn may append, change or reorder items.
	Update(fn func(items []Item) ([]Item, error)) error
	// SaveSession writes an item's redacted session and preview.
	SaveSession(id string, s *traceimport.Session, preview []byte) error
	// Session reads an item's redacted session.
	Session(id string) (*traceimport.Session, error)
	// Preview reads an item's preview.
	Preview(id string) ([]byte, error)
	// Cursors returns every source's cursor.
	Cursors() (map[string]Cursor, error)
	// SaveCursor replaces one source's cursor.
	SaveCursor(source string, c Cursor) error
	// Log appends one event to the log.
	Log(ev LogEvent) error
}

// Cursor is how far a source's pulls got. Since is the newest flag time
// seen; Seen holds the trace ids flagged at or after Since minus the
// overlap, so the overlap never counts a trace twice; Files holds the byte
// offsets read in an OTLP folder.
type Cursor struct {
	Since     time.Time            `json:"since,omitzero"`
	Seen      map[string]time.Time `json:"seen,omitempty"`
	Files     map[string]int64     `json:"files,omitempty"`
	LastPull  time.Time            `json:"last_pull,omitzero"`
	LastError string               `json:"last_error,omitempty"`
	Pulled    int                  `json:"pulled,omitempty"` // sessions taken in by the last pull
}

// maxSeen caps the remembered trace ids of one cursor.
const maxSeen = 10000

// prune drops seen ids older than keep before Since, and the oldest past
// maxSeen.
func (c *Cursor) prune(keep time.Duration) {
	cut := c.Since.Add(-keep)
	for id, at := range c.Seen {
		if at.Before(cut) {
			delete(c.Seen, id)
		}
	}
	if len(c.Seen) <= maxSeen {
		return
	}
	type kv struct {
		id string
		at time.Time
	}
	all := make([]kv, 0, len(c.Seen))
	for id, at := range c.Seen {
		all = append(all, kv{id, at})
	}
	slices.SortFunc(all, func(a, b kv) int { return a.at.Compare(b.at) })
	for _, e := range all[:len(all)-maxSeen] {
		delete(c.Seen, e.id)
	}
}

// LogEvent is one line of log.jsonl: a pull's outcome or a state change.
type LogEvent struct {
	At     time.Time `json:"at"`
	Event  string    `json:"event"` // pull | ingest | accept | reject | duplicate | snooze | recur
	Item   string    `json:"item,omitempty"`
	Source string    `json:"source,omitempty"`
	Detail string    `json:"detail,omitempty"`
}

// FileStore keeps the inbox as files under one folder. A flock on .lock
// serializes writers across processes; a mutex within one.
type FileStore struct {
	dir string
	mu  sync.Mutex
}

// NewFileStore returns a store rooted at dir (the project's inbox folder).
// Nothing is created until the first write.
func NewFileStore(dir string) *FileStore { return &FileStore{dir: dir} }

// Dir is the inbox folder.
func (st *FileStore) Dir() string { return st.dir }

var itemIDPattern = regexp.MustCompile(`^inc-[0-9a-f]{12}$`)

// ValidItemID reports whether id is an inbox item id.
func ValidItemID(id string) bool { return itemIDPattern.MatchString(id) }

// Items implements Store.
func (st *FileStore) Items() ([]Item, error) {
	return st.readItems()
}

func (st *FileStore) readItems() ([]Item, error) {
	f, err := os.Open(filepath.Join(st.dir, ItemsFile))
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read inbox items: %w", err)
	}
	defer f.Close()
	var out []Item
	sc := bufio.NewScanner(io.LimitReader(f, maxItemsFileBytes))
	sc.Buffer(make([]byte, 0, 64<<10), 4<<20)
	n := 0
	for sc.Scan() {
		n++
		line := bytes.TrimSpace(sc.Bytes())
		if len(line) == 0 {
			continue
		}
		var it Item
		if err := json.Unmarshal(line, &it); err != nil {
			return nil, fmt.Errorf("read inbox items: line %d: %w", n, err)
		}
		out = append(out, it)
	}
	if err := sc.Err(); err != nil {
		return nil, fmt.Errorf("read inbox items: %w", err)
	}
	return out, nil
}

// Update implements Store.
func (st *FileStore) Update(fn func(items []Item) ([]Item, error)) error {
	unlock, err := st.lock()
	if err != nil {
		return err
	}
	defer unlock()
	items, err := st.readItems()
	if err != nil {
		return err
	}
	out, err := fn(items)
	if err != nil {
		return err
	}
	if len(out) > MaxItems {
		return fmt.Errorf("the inbox holds %d items, over the %d cap; reject or accept old items first", len(out), MaxItems)
	}
	var buf bytes.Buffer
	for i := range out {
		line, err := json.Marshal(&out[i])
		if err != nil {
			return fmt.Errorf("encode item %s: %w", out[i].ID, err)
		}
		buf.Write(line)
		buf.WriteByte('\n')
	}
	return writeAtomic(filepath.Join(st.dir, ItemsFile), buf.Bytes())
}

// SaveSession implements Store. The session is redacted before it gets
// here; a session over MaxSessionBytes is refused.
func (st *FileStore) SaveSession(id string, s *traceimport.Session, preview []byte) error {
	if !ValidItemID(id) {
		return fmt.Errorf("item id %q is not valid", id)
	}
	data, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return fmt.Errorf("encode session of %s: %w", id, err)
	}
	if len(data) > MaxSessionBytes {
		return fmt.Errorf("session of %s is %d bytes, over the %d byte cap", id, len(data), MaxSessionBytes)
	}
	if len(preview) > MaxPreviewBytes {
		preview = preview[:MaxPreviewBytes]
	}
	dir := filepath.Join(st.dir, ItemsDir, id)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	if err := writeAtomic(filepath.Join(dir, SessionFile), append(data, '\n')); err != nil {
		return err
	}
	return writeAtomic(filepath.Join(dir, PreviewFile), preview)
}

// Session implements Store.
func (st *FileStore) Session(id string) (*traceimport.Session, error) {
	if !ValidItemID(id) {
		return nil, fmt.Errorf("item %q: %w", id, ErrNotFound)
	}
	data, err := readCapped(filepath.Join(st.dir, ItemsDir, id, SessionFile), MaxSessionBytes+1)
	if errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("session of item %s: %w", id, ErrNotFound)
	}
	if err != nil {
		return nil, err
	}
	var s traceimport.Session
	if err := json.Unmarshal(data, &s); err != nil {
		return nil, fmt.Errorf("session of item %s: %w", id, err)
	}
	return &s, nil
}

// Preview implements Store.
func (st *FileStore) Preview(id string) ([]byte, error) {
	if !ValidItemID(id) {
		return nil, fmt.Errorf("item %q: %w", id, ErrNotFound)
	}
	data, err := readCapped(filepath.Join(st.dir, ItemsDir, id, PreviewFile), MaxPreviewBytes)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	return data, err
}

// Cursors implements Store.
func (st *FileStore) Cursors() (map[string]Cursor, error) {
	data, err := readCapped(filepath.Join(st.dir, CursorsFile), 64<<20)
	if errors.Is(err, os.ErrNotExist) {
		return map[string]Cursor{}, nil
	}
	if err != nil {
		return nil, err
	}
	out := map[string]Cursor{}
	if err := json.Unmarshal(data, &out); err != nil {
		return nil, fmt.Errorf("read inbox cursors: %w", err)
	}
	return out, nil
}

// SaveCursor implements Store.
func (st *FileStore) SaveCursor(source string, c Cursor) error {
	unlock, err := st.lock()
	if err != nil {
		return err
	}
	defer unlock()
	all, err := st.Cursors()
	if err != nil {
		return err
	}
	all[source] = c
	data, err := json.MarshalIndent(all, "", "  ")
	if err != nil {
		return err
	}
	return writeAtomic(filepath.Join(st.dir, CursorsFile), append(data, '\n'))
}

// Log implements Store. A line is capped; the log is append-only.
func (st *FileStore) Log(ev LogEvent) error {
	line, err := json.Marshal(ev)
	if err != nil {
		return err
	}
	if len(line) > maxLogLineBytes {
		ev.Detail = ev.Detail[:min(len(ev.Detail), 512)] + "..."
		if line, err = json.Marshal(ev); err != nil {
			return err
		}
	}
	if err := os.MkdirAll(st.dir, 0o755); err != nil {
		return err
	}
	f, err := os.OpenFile(filepath.Join(st.dir, LogFile), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	_, werr := f.Write(append(line, '\n'))
	return errors.Join(werr, f.Close())
}

// lock takes the store's mutex and its flock.
func (st *FileStore) lock() (func(), error) {
	st.mu.Lock()
	if err := os.MkdirAll(st.dir, 0o755); err != nil {
		st.mu.Unlock()
		return nil, fmt.Errorf("create inbox folder: %w", err)
	}
	f, err := os.OpenFile(filepath.Join(st.dir, lockFileName), os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		st.mu.Unlock()
		return nil, fmt.Errorf("open inbox lock: %w", err)
	}
	release, err := flock(f)
	if err != nil {
		f.Close()
		st.mu.Unlock()
		return nil, err
	}
	return func() {
		_ = release()
		st.mu.Unlock()
	}, nil
}

// writeAtomic writes data through a temporary file and a rename.
func writeAtomic(path string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".tmp-*")
	if err != nil {
		return err
	}
	_, werr := tmp.Write(data)
	if werr == nil {
		werr = tmp.Sync()
	}
	if err := errors.Join(werr, tmp.Close()); err != nil {
		return errors.Join(err, os.Remove(tmp.Name()))
	}
	if err := os.Rename(tmp.Name(), path); err != nil {
		return errors.Join(err, os.Remove(tmp.Name()))
	}
	return nil
}

// readCapped reads at most limit bytes of a regular file.
func readCapped(path string, limit int64) ([]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("%s is not a regular file: %w", path, fs.ErrInvalid)
	}
	data, err := io.ReadAll(io.LimitReader(f, limit))
	if err != nil {
		return nil, err
	}
	return data, nil
}
