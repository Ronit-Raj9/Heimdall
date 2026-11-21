package improve

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"time"

	"github.com/maximhq/bifrost/bench/utils"
)

// LineageFile is the hash-chained lineage log in an improvement's folder.
const LineageFile = "lineage.jsonl"

// Lineage entry statuses.
const (
	NodeBase      = "base"      // version 0
	NodeCommitted = "committed" // became the incumbent
	NodeRejected  = "rejected"  // screened out, failed the gate, or blocked
	NodeTripped   = "tripped"   // a tripwire fired and nobody approved it
	NodeControl   = "control"   // a null-improver version, never an incumbent
	NodePending   = "pending"   // frozen, decision not made yet
)

// Node is one lineage entry: one frozen version and the decision it got. A
// version can appear twice (frozen pending, then decided); the last entry
// of a version is its status. Hash covers every other field and PrevHash,
// so a rewrite anywhere breaks the chain from there on.
type Node struct {
	Seq        int       `json:"seq"`
	Round      int       `json:"round"`
	Ref        string    `json:"ref"`
	Version    int       `json:"version"`
	Parent     string    `json:"parent,omitempty"` // parent's ref
	Author     string    `json:"author"`           // human | agent
	BundleHash string    `json:"bundle_hash"`
	ConfigHash string    `json:"config_hash"`
	Status     string    `json:"status"`
	Reason     string    `json:"reason"`
	ProducedBy string    `json:"produced_by,omitempty"` // improver run id
	At         time.Time `json:"at"`
	PrevHash   string    `json:"prev_hash"`
	Hash       string    `json:"hash"`
}

// nodeHash is the hash of n with Hash cleared.
func nodeHash(n Node) (string, error) {
	n.Hash = ""
	return utils.HashJSON(n)
}

// appendNode chains n onto the log in dir and returns it as written.
func appendNode(dir string, n Node) (Node, error) {
	nodes, err := ReadLineage(dir)
	if err != nil {
		return n, err
	}
	n.Seq = len(nodes) + 1
	if len(nodes) > 0 {
		n.PrevHash = nodes[len(nodes)-1].Hash
	}
	if n.At.IsZero() {
		n.At = time.Now().UTC()
	}
	if n.Hash, err = nodeHash(n); err != nil {
		return n, err
	}
	b, err := json.Marshal(n)
	if err != nil {
		return n, err
	}
	f, err := os.OpenFile(filepath.Join(dir, LineageFile), os.O_WRONLY|os.O_APPEND|os.O_CREATE, 0o644)
	if err != nil {
		return n, err
	}
	if _, err := f.Write(append(b, '\n')); err != nil {
		return n, errors.Join(err, f.Close())
	}
	if err := f.Sync(); err != nil {
		return n, errors.Join(err, f.Close())
	}
	return n, f.Close()
}

// ReadLineage reads and verifies the chain in dir. A missing log is empty;
// a broken chain is an error naming the first bad entry.
func ReadLineage(dir string) ([]Node, error) {
	b, err := os.ReadFile(filepath.Join(dir, LineageFile))
	if errors.Is(err, fs.ErrNotExist) {
		return []Node{}, nil
	}
	if err != nil {
		return nil, err
	}
	out := []Node{}
	sc := bufio.NewScanner(bytes.NewReader(b))
	sc.Buffer(make([]byte, 64*1024), 4*1024*1024)
	prev := ""
	for sc.Scan() {
		line := bytes.TrimSpace(sc.Bytes())
		if len(line) == 0 {
			continue
		}
		var n Node
		if err := json.Unmarshal(line, &n); err != nil {
			return out, fmt.Errorf("lineage entry %d: %w", len(out)+1, err)
		}
		h, err := nodeHash(n)
		if err != nil {
			return out, err
		}
		if n.PrevHash != prev || n.Hash != h || n.Seq != len(out)+1 {
			return out, fmt.Errorf("lineage entry %d does not verify: the chain was changed", len(out)+1)
		}
		prev = n.Hash
		out = append(out, n)
	}
	return out, sc.Err()
}
