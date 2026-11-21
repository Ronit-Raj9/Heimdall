package schemas

import (
	"fmt"
	"path"
	"regexp"
	"strconv"
	"time"
)

// AgentVersionsDir is where frozen agent config snapshots live, relative to
// the project root: <dir>/<id>/v<N>.yaml plus <dir>/<id>/versions.json.
const AgentVersionsDir = ".heimdall/agent-versions"

// AgentVersion is one entry of an agent's versions.json.
type AgentVersion struct {
	Version   int       `json:"version"`
	Hash      string    `json:"hash"`
	CreatedAt time.Time `json:"created_at"`
	CreatedBy string    `json:"created_by"`
	Note      string    `json:"note"`
	// FileVersion and FileSHA256 link the version to the Files API history
	// of agents/<id>.yaml (GET /project/versions/agents/<id>.yaml): the
	// project file version this snapshot was taken from.
	FileVersion string `json:"file_version,omitempty"`
	FileSHA256  string `json:"file_sha256,omitempty"`
	// Lineage of a version an improvement run produced (bench/improve):
	// the version it was derived from, who wrote it (AuthorHuman or
	// AuthorAgent), the improvement run, its round, the improver's run and
	// the hash of the editable bundle. All empty for a human save.
	Parent        int    `json:"parent,omitempty"`
	Author        string `json:"author,omitempty"`
	Improvement   string `json:"improvement,omitempty"`
	Round         int    `json:"round,omitempty"`
	ProducedByRun string `json:"produced_by_run,omitempty"`
	BundleHash    string `json:"bundle_hash,omitempty"`
}

// Authors of an agent version (AgentVersion.Author).
const (
	AuthorHuman = "human"
	AuthorAgent = "agent"
)

// agentVersionRef is agents/<id>.yaml@v<N> (or .yml).
var agentVersionRef = regexp.MustCompile(`^agents/([A-Za-z0-9][A-Za-z0-9._-]{0,62})\.ya?ml@v([1-9][0-9]{0,8})$`)

// ParseAgentVersionRef splits an agent path pinned to a version,
// agents/<id>.yaml@v<N>, into the plain path, the agent id and N. ok is false
// for any other string (a plain path included).
func ParseAgentVersionRef(ref string) (base, id string, version int, ok bool) {
	m := agentVersionRef.FindStringSubmatch(ref)
	if m == nil {
		return "", "", 0, false
	}
	n, err := strconv.Atoi(m[2])
	if err != nil {
		return "", "", 0, false
	}
	base = ref[:len(ref)-len("@v")-len(m[2])]
	return base, m[1], n, true
}

// AgentVersionSnapshotPath is the project-relative path of version n of
// agent id.
func AgentVersionSnapshotPath(id string, n int) string {
	return path.Join(AgentVersionsDir, id, fmt.Sprintf("v%d.yaml", n))
}
