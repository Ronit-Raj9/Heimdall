package improve

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"

	"github.com/maximhq/bifrost/bench/utils"
)

// ObjectKind names what a content-addressed object holds.
type ObjectKind string

// Object kinds.
const (
	ObjBundle    ObjectKind = "bundle"
	ObjPlaybook  ObjectKind = "playbook"
	ObjCandidate ObjectKind = "candidate"
	ObjMeasure   ObjectKind = "measure"
	ObjEdits     ObjectKind = "edits"
)

// ErrObjectMismatch reports a stored object whose bytes do not hash to its name.
var ErrObjectMismatch = errors.New("stored object does not match its hash")

// Objects is the write-once store of immutable objects every round depends
// on: bundles, playbooks, candidates, edit sets and measurement specs, each
// named by the sha256 of its canonical JSON. Root is
// <runs root>/.improvements/objects; it is shared by every improvement and
// study of the runs root.
type Objects struct{ Root string }

// objectEnvelope is the stored form: the kind is part of the hashed bytes,
// so equal values of two kinds are two objects.
type objectEnvelope struct {
	Kind ObjectKind      `json:"kind"`
	V    json.RawMessage `json:"v"`
}

var objectHashRe = regexp.MustCompile(`^[0-9a-f]{64}$`)

func (o Objects) path(hash string) string {
	return filepath.Join(o.Root, "sha256", hash[:2], hash+".json")
}

// Put wraps v as {"kind":kind,"v":v}, hashes its canonical JSON and writes it
// once. An existing file must hold the same bytes, else ErrObjectMismatch.
func (o Objects) Put(kind ObjectKind, v any) (string, error) {
	if o.Root == "" {
		return "", errors.New("the object store has no folder")
	}
	inner, err := utils.CanonicalJSON(v)
	if err != nil {
		return "", err
	}
	data, err := utils.CanonicalJSON(objectEnvelope{Kind: kind, V: inner})
	if err != nil {
		return "", err
	}
	hash := utils.SHA256Bytes(data)
	p := o.path(hash)
	old, err := os.ReadFile(p)
	switch {
	case err == nil:
		if !bytes.Equal(old, data) {
			return "", fmt.Errorf("object %s: %w", hash, ErrObjectMismatch)
		}
		return hash, nil
	case !errors.Is(err, fs.ErrNotExist):
		return "", err
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return "", err
	}
	if err := writeAtomic(p, data, 0o644); err != nil {
		return "", err
	}
	return hash, nil
}

// Get reads the object named hash into into. A missing object is
// ErrNotFound; bytes that do not hash to the name are ErrObjectMismatch; an
// object of another kind is an error.
func (o Objects) Get(hash string, kind ObjectKind, into any) error {
	if !objectHashRe.MatchString(hash) {
		return fmt.Errorf("object hash %q is not a sha256 hex digest", hash)
	}
	data, err := os.ReadFile(o.path(hash))
	if errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("object %s: %w", hash, ErrNotFound)
	}
	if err != nil {
		return err
	}
	if utils.SHA256Bytes(data) != hash {
		return fmt.Errorf("object %s: %w", hash, ErrObjectMismatch)
	}
	var env objectEnvelope
	if err := json.Unmarshal(data, &env); err != nil {
		return fmt.Errorf("object %s: %w", hash, err)
	}
	if env.Kind != kind {
		return fmt.Errorf("object %s is a %s, not a %s", hash, env.Kind, kind)
	}
	return json.Unmarshal(env.V, into)
}

// Has reports whether an object named hash is stored.
func (o Objects) Has(hash string) bool {
	if !objectHashRe.MatchString(hash) {
		return false
	}
	_, err := os.Stat(o.path(hash))
	return err == nil
}

// BundleObject is a complete agent bundle.
type BundleObject struct {
	Files map[string]string `json:"files"` // container path (or InstructionsKey) -> exact text, never rendered
	Raw   []string          `json:"raw"`   // paths delivered verbatim (files_raw), sorted
}

// PlaybookObject is the numbered rule list of a bundle.
type PlaybookObject struct {
	Next  int            `json:"next"` // next item number
	Items []PlaybookItem `json:"items"`
}

// PlaybookItem is one numbered rule.
type PlaybookItem struct {
	ID          string   `json:"id"` // pb-0001
	File        string   `json:"file"`
	Section     string   `json:"section"`
	Text        string   `json:"text"`
	OriginRound int      `json:"origin_round"`
	Evidence    []string `json:"evidence"` // run refs "dev:<task>#<trial>"
	Helpful     int      `json:"helpful"`
	Harmful     int      `json:"harmful"`
}

// CandidateObject is one candidate version: where it came from and what it holds.
type CandidateObject struct {
	Source       string   `json:"source"`                  // v0 | mutation | merge | restart | null
	ParentBundle string   `json:"parent_bundle"`           // object hash
	SecondBundle string   `json:"second_bundle,omitempty"` // merge only
	Bundle       string   `json:"bundle"`
	Playbook     string   `json:"playbook"`
	Edits        string   `json:"edits"` // ObjEdits hash; "" for v0
	ConfigHash   string   `json:"config_hash"`
	Improver     string   `json:"improver"` // suite lock hash of the improver task
	Model        string   `json:"model"`    // the model the improver ran on
	RunIDs       []string `json:"run_ids"`
	Round        int      `json:"round"`
	Slot         int      `json:"slot"`
}

// MeasureObject is what one measurement was planned to be, stored before
// its experiment starts. Audit and sealed measures hold only a task list
// hash and count, never task ids.
type MeasureObject struct {
	Phase     string   `json:"phase"` // baseline | screen | audit | sealed | final | extension | retries
	Round     int      `json:"round"`
	Look      int      `json:"look,omitempty"`
	Arms      []string `json:"arms"`       // candidate object hashes, A first
	TasksHash string   `json:"tasks_hash"` // utils.HashJSON(sorted task ids)
	NTasks    int      `json:"n_tasks"`
	Trials    int      `json:"trials"`
	Level     float64  `json:"level,omitempty"`
	Seed      int64    `json:"seed"`
	SpecHash  string   `json:"spec_hash"`
}
