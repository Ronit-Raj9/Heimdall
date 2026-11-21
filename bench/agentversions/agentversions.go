// Package agentversions keeps frozen snapshots of agent configs under the
// project's .heimdall/agent-versions/<id>/ folder: v<N>.yaml per version and
// versions.json as the index. A save whose config hash equals the latest
// version's adds nothing. Built-in library agents get one synthetic,
// read-only v1 (their library file).
package agentversions

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/maximhq/bifrost/bench/suite"
	"github.com/maximhq/bifrost/bench/utils"
	"gopkg.in/yaml.v3"
)

// ErrNotFound is returned for an unknown agent or version.
var ErrNotFound = errors.New("agent version not found")

// LibraryActor is CreatedBy of a library agent's synthetic v1.
const LibraryActor = "library"

var idRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,62}$`)

// ValidID reports whether id is safe as a single path segment.
func ValidID(id string) bool { return idRe.MatchString(id) }

// ConfigHash is the planner's config hash of an agent YAML document with no
// overrides: the document is resolved exactly as engine.ResolveAgentConfig
// does (YAML tree round trip, schema-checked decode) and hashed with
// utils.HashJSON, so it equals a manifest arm's config_hash for agents whose
// image is not built per suite.
func ConfigHash(doc []byte) (string, error) {
	cfg, err := decodeConfig(doc)
	if err != nil {
		return "", err
	}
	return utils.HashJSON(&cfg)
}

// RepeatArmEnv is the env var the evaluation planner sets on the hidden
// repeat arm of a score-mode evaluation (handlers/evaluations.go evalArms):
// the same agent, so it is no part of the version identity.
const RepeatArmEnv = "HEIMDALL_QUICKRUN_ARM"

// AgentHash is the version identity of a resolved agent config: its
// config hash with the axes an evaluation sets per arm left out (model and
// gateway provider), the repeat arm's marker env var dropped, and, for an
// agent whose image is built per suite, the resolved image tag dropped (its
// build section stays). A run of a version on any model, and the version's
// own snapshot, hash the same.
func AgentHash(cfg schemas.AgentConfig) (string, error) {
	cfg.Model = ""
	cfg.Gateway.Provider = ""
	if cfg.Build != nil {
		cfg.Image = ""
	}
	if _, ok := cfg.Env[RepeatArmEnv]; ok {
		env := make(map[string]string, len(cfg.Env))
		for k, v := range cfg.Env {
			if k != RepeatArmEnv {
				env[k] = v
			}
		}
		cfg.Env = env
	}
	if len(cfg.Env) == 0 {
		cfg.Env = nil
	}
	return utils.HashJSON(&cfg)
}

// DocAgentHash is AgentHash of an agent YAML document, decoded as
// ConfigHash decodes it.
func DocAgentHash(doc []byte) (string, error) {
	cfg, err := decodeConfig(doc)
	if err != nil {
		return "", err
	}
	return AgentHash(cfg)
}

// decodeConfig decodes an agent YAML document as engine.ResolveAgentConfig
// does with no overrides.
func decodeConfig(doc []byte) (schemas.AgentConfig, error) {
	var cfg schemas.AgentConfig
	var tree map[string]any
	if err := yaml.Unmarshal(doc, &tree); err != nil {
		return cfg, fmt.Errorf("parse agent config: %w", err)
	}
	if tree == nil {
		tree = map[string]any{}
	}
	out, err := yaml.Marshal(tree)
	if err != nil {
		return cfg, fmt.Errorf("encode agent config: %w", err)
	}
	if err := suite.DecodeYAML(out, "effective agent config", suite.SchemaAgent, &cfg); err != nil {
		return cfg, err
	}
	return cfg, nil
}

// VersionOf returns the version of agent id whose agent hash is
// agentHash: the newest such project version, else 1 for a built-in
// library agent (under lib) the project does not version whose library
// file hashes the same, else 0.
func VersionOf(root, lib, id, agentHash string) int {
	if agentHash == "" || !ValidID(id) {
		return 0
	}
	agentHash = strings.TrimPrefix(agentHash, "sha256:")
	if root != "" {
		if vs, err := List(root, id); err == nil && len(vs) > 0 {
			for i := len(vs) - 1; i >= 0; i-- {
				if strings.TrimPrefix(vs[i].AgentHash, "sha256:") == agentHash {
					return vs[i].Version
				}
			}
			return 0
		}
	}
	if p := LibraryFile(lib, id); p != "" {
		if _, v, err := LibraryVersion(p); err == nil && strings.TrimPrefix(v.AgentHash, "sha256:") == agentHash {
			return 1
		}
	}
	return 0
}

var (
	locksMu sync.Mutex
	locks   = map[string]*sync.Mutex{}
)

func lockFor(dir string) func() {
	locksMu.Lock()
	m, ok := locks[dir]
	if !ok {
		m = &sync.Mutex{}
		locks[dir] = m
	}
	locksMu.Unlock()
	m.Lock()
	return m.Unlock
}

func agentDir(root, id string) string {
	return filepath.Join(root, filepath.FromSlash(schemas.AgentVersionsDir), id)
}

// List returns the stored versions of agent id, oldest first. No folder is
// an empty list.
func List(root, id string) ([]schemas.AgentVersion, error) {
	if !ValidID(id) {
		return nil, fmt.Errorf("agent id %q is not valid", id)
	}
	return readIndex(agentDir(root, id))
}

func readIndex(dir string) ([]schemas.AgentVersion, error) {
	b, err := os.ReadFile(filepath.Join(dir, "versions.json"))
	if errors.Is(err, fs.ErrNotExist) {
		return []schemas.AgentVersion{}, nil
	}
	if err != nil {
		return nil, err
	}
	var out []schemas.AgentVersion
	if err := json.Unmarshal(b, &out); err != nil {
		return nil, fmt.Errorf("versions.json: %w", err)
	}
	if out == nil {
		out = []schemas.AgentVersion{}
	}
	for i := range out {
		if out[i].AgentHash != "" {
			continue
		}
		// An entry from before agent hashes: fill it from its snapshot.
		if doc, err := os.ReadFile(filepath.Join(dir, fmt.Sprintf("v%d.yaml", out[i].Version))); err == nil {
			out[i].AgentHash, _ = DocAgentHash(doc)
		}
	}
	return out, nil
}

// Read returns the YAML of version n of agent id and its index entry.
func Read(root, id string, n int) ([]byte, schemas.AgentVersion, error) {
	vs, err := List(root, id)
	if err != nil {
		return nil, schemas.AgentVersion{}, err
	}
	for _, v := range vs {
		if v.Version != n {
			continue
		}
		b, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(schemas.AgentVersionSnapshotPath(id, n))))
		if errors.Is(err, fs.ErrNotExist) {
			return nil, v, ErrNotFound
		}
		return b, v, err
	}
	return nil, schemas.AgentVersion{}, ErrNotFound
}

// Source is one saved content of agents/<id>.yaml, as the Files API
// history records it.
type Source struct {
	Doc         []byte
	At          time.Time // zero: now
	Actor, Note string
	FileVersion string // Files API version id, "" when unknown
	FileSHA256  string
}

// Snapshot freezes doc as the next version of agent id unless its config
// hash equals the latest version's. It returns the version that now holds
// doc and whether it was created.
func Snapshot(root, id string, doc []byte, actor, note string) (schemas.AgentVersion, bool, error) {
	return Record(root, id, []Source{{Doc: doc, Actor: actor, Note: note}})
}

// Record adds versions from the Files API history of agents/<id>.yaml,
// oldest first; the last entry is the current content. An agent with no
// versions yet is backfilled from the whole history (each change of config
// hash is a version, a document that does not decode is skipped); otherwise
// only the last entry is considered. A config hash equal to the latest
// version's adds nothing. It returns the version that holds the last entry
// and whether any version was created.
func Record(root, id string, history []Source) (schemas.AgentVersion, bool, error) {
	if !ValidID(id) {
		return schemas.AgentVersion{}, false, fmt.Errorf("agent id %q is not valid", id)
	}
	if len(history) == 0 {
		return schemas.AgentVersion{}, false, errors.New("nothing to record")
	}
	dir := agentDir(root, id)
	defer lockFor(dir)()
	vs, err := readIndex(dir)
	if err != nil {
		return schemas.AgentVersion{}, false, err
	}
	if len(vs) > 0 {
		history = history[len(history)-1:]
	}
	// The current content must decode; older history entries may not.
	lastHash, lastAgent, err := hashes(history[len(history)-1].Doc)
	if err != nil {
		return schemas.AgentVersion{}, false, err
	}
	created := false
	for i, src := range history {
		hash, agentHash := lastHash, lastAgent
		if i < len(history)-1 {
			if hash, agentHash, err = hashes(src.Doc); err != nil {
				continue
			}
		}
		// A version is the agent without its model and provider (AgentHash):
		// a change of default model alone is no new version, since every
		// evaluation sets the model per arm anyway.
		if len(vs) > 0 && (vs[len(vs)-1].Hash == hash || vs[len(vs)-1].AgentHash == agentHash) {
			continue
		}
		next := 1
		if len(vs) > 0 {
			next = vs[len(vs)-1].Version + 1
		}
		at := src.At.UTC()
		if src.At.IsZero() {
			at = time.Now().UTC()
		}
		note := src.Note
		if len(note) > 200 {
			note = note[:200]
		}
		v := schemas.AgentVersion{Version: next, Hash: hash, AgentHash: agentHash, CreatedAt: at, CreatedBy: src.Actor, Note: note,
			FileVersion: src.FileVersion, FileSHA256: src.FileSHA256}
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return v, created, err
		}
		if err := writeAtomic(filepath.Join(dir, fmt.Sprintf("v%d.yaml", next)), src.Doc); err != nil {
			return v, created, err
		}
		vs, created = append(vs, v), true
	}
	if created {
		b, err := json.MarshalIndent(vs, "", "  ")
		if err != nil {
			return vs[len(vs)-1], created, err
		}
		if err := writeAtomic(filepath.Join(dir, "versions.json"), append(b, '\n')); err != nil {
			return vs[len(vs)-1], created, err
		}
	}
	return vs[len(vs)-1], created, nil
}

// hashes returns a document's config hash and agent hash.
func hashes(doc []byte) (hash, agentHash string, err error) {
	cfg, err := decodeConfig(doc)
	if err != nil {
		return "", "", err
	}
	if hash, err = utils.HashJSON(&cfg); err != nil {
		return "", "", err
	}
	agentHash, err = AgentHash(cfg)
	return hash, agentHash, err
}

// Derive freezes doc as a version produced from version parent of agent id
// (an improvement run's candidate). lin carries the lineage fields; Version,
// Hash and CreatedAt are set here. A doc whose config hash equals any
// stored version's (or whose agent hash does: the same agent on another
// model) adds nothing and returns that version, so a candidate identical to
// an earlier one keeps its first lineage.
func Derive(root, id string, doc []byte, lin schemas.AgentVersion) (schemas.AgentVersion, bool, error) {
	if !ValidID(id) {
		return schemas.AgentVersion{}, false, fmt.Errorf("agent id %q is not valid", id)
	}
	hash, agentHash, err := hashes(doc)
	if err != nil {
		return schemas.AgentVersion{}, false, err
	}
	dir := agentDir(root, id)
	defer lockFor(dir)()
	vs, err := readIndex(dir)
	if err != nil {
		return schemas.AgentVersion{}, false, err
	}
	// A version is the agent without its model and provider (AgentHash), as
	// in Record: a candidate that only changes the model is that version.
	for _, v := range vs {
		if v.Hash == hash || (agentHash != "" && v.AgentHash == agentHash) {
			return v, false, nil
		}
	}
	if lin.Parent != 0 && !slices.ContainsFunc(vs, func(v schemas.AgentVersion) bool { return v.Version == lin.Parent }) {
		return schemas.AgentVersion{}, false, fmt.Errorf("agent %s has no parent version v%d", id, lin.Parent)
	}
	v := lin
	v.Version, v.Hash, v.AgentHash, v.CreatedAt = 1, hash, agentHash, time.Now().UTC()
	if len(vs) > 0 {
		v.Version = vs[len(vs)-1].Version + 1
	}
	if len(v.Note) > 200 {
		v.Note = v.Note[:200]
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return v, false, err
	}
	if err := writeAtomic(filepath.Join(dir, fmt.Sprintf("v%d.yaml", v.Version)), doc); err != nil {
		return v, false, err
	}
	b, err := json.MarshalIndent(append(vs, v), "", "  ")
	if err != nil {
		return v, false, err
	}
	if err := writeAtomic(filepath.Join(dir, "versions.json"), append(b, '\n')); err != nil {
		return v, false, err
	}
	return v, true, nil
}

func writeAtomic(path string, data []byte) error {
	f, err := os.CreateTemp(filepath.Dir(path), ".tmp-*")
	if err != nil {
		return err
	}
	tmp := f.Name()
	if _, err := f.Write(data); err != nil {
		f.Close()
		os.Remove(tmp)
		return err
	}
	if err := f.Close(); err != nil {
		os.Remove(tmp)
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		os.Remove(tmp)
		return err
	}
	return nil
}

// LibraryFile returns the library agent file agents/<id>.yaml (or .yml)
// under lib, or "" when there is none.
func LibraryFile(lib, id string) string {
	if lib == "" || !ValidID(id) {
		return ""
	}
	for _, ext := range []string{".yaml", ".yml"} {
		p := filepath.Join(lib, "agents", id+ext)
		if fi, err := os.Stat(p); err == nil && fi.Mode().IsRegular() {
			return p
		}
	}
	return ""
}

// LibraryVersion returns the synthetic v1 of a library agent file.
func LibraryVersion(path string) ([]byte, schemas.AgentVersion, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, schemas.AgentVersion{}, err
	}
	v := schemas.AgentVersion{Version: 1, CreatedBy: LibraryActor, Note: "built-in agent"}
	if fi, err := os.Stat(path); err == nil {
		v.CreatedAt = fi.ModTime().UTC()
	}
	if v.Hash, v.AgentHash, err = hashes(b); err != nil {
		v.Hash, v.AgentHash = utils.SHA256Bytes(b), ""
	}
	return b, v, nil
}

// Register freezes doc as a new version of agent id without touching the
// agent's current config: a pull request's candidate config, registered by
// `bifrost-bench ci evaluate`. Unlike Snapshot it reuses ANY existing version
// with the same config hash, not only the latest, so re-running CI on the
// same commit, or on a pull request that restores the live config, adds
// nothing. It returns the version that holds doc and whether it was created.
func Register(root, id string, doc []byte, actor, note string) (schemas.AgentVersion, bool, error) {
	if !ValidID(id) {
		return schemas.AgentVersion{}, false, fmt.Errorf("agent id %q is not valid", id)
	}
	hash, err := ConfigHash(doc)
	if err != nil {
		return schemas.AgentVersion{}, false, err
	}
	dir := agentDir(root, id)
	unlock := lockFor(dir)
	vs, err := readIndex(dir)
	unlock()
	if err != nil {
		return schemas.AgentVersion{}, false, err
	}
	for i := len(vs) - 1; i >= 0; i-- {
		if vs[i].Hash == hash {
			return vs[i], false, nil
		}
	}
	return Record(root, id, []Source{{Doc: doc, Actor: actor, Note: note}})
}

// Live returns the newest version whose config hash equals current, the
// agent's saved config (agents/<id>.yaml): the version the agent page runs
// and the baseline a pull request is compared with. It returns 0 when no
// version holds current.
func Live(vs []schemas.AgentVersion, current []byte) int {
	hash, err := ConfigHash(current)
	if err != nil {
		return 0
	}
	for i := len(vs) - 1; i >= 0; i-- {
		if vs[i].Hash == hash {
			return vs[i].Version
		}
	}
	return 0
}
