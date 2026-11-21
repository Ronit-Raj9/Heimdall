package main

// A20 CLI parity for agent versions (agent-first contract, section 2).
//
//	bifrost-bench agent versions <id> [--json]
//	bifrost-bench agent snapshot <id> [--note "..."] [--json]
//
// With a server configured these call GET/POST /agents/{id}/versions. Without
// one they read and write .heimdall/agent-versions/<id>/ (v<N>.yaml plus
// versions.json) next to the local agents/<id>.yaml.

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"text/tabwriter"
	"time"
)

// agentVersion is one row of versions.json and of the versions API.
type agentVersion struct {
	Version   int       `json:"version"`
	Hash      string    `json:"hash"`
	CreatedAt time.Time `json:"created_at"`
	CreatedBy string    `json:"created_by,omitempty"`
	Note      string    `json:"note"`
	Runs      int       `json:"runs,omitempty"`
}

type agentVersions struct {
	Agent    string         `json:"agent"`
	Versions []agentVersion `json:"versions"`
}

func runAgentVersionsCmd(sub string, args []string, out io.Writer) int {
	fs := flag.NewFlagSet("agent "+sub, flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	var c connOpts
	addConnFlags(fs, &c)
	asJSON := fs.Bool("json", false, "print JSON")
	note := ""
	if sub == "snapshot" {
		fs.StringVar(&note, "note", "", "what changed in this version")
	}
	usage := "usage: " + progName + " agent versions <id> [-json] | agent snapshot <id> [-note text] [-json]  (-server URL -token TOKEN, or local -config)"
	pos, err := parseInterspersed(fs, args)
	if err != nil || len(pos) != 1 {
		if errors.Is(err, flag.ErrHelp) {
			fmt.Fprintln(out, usage)
			return exitOK
		}
		fmt.Fprintln(os.Stderr, usage)
		return exitUsage
	}
	c.finish(fs)
	id, _, err := agentRef(pos[0])
	if err != nil || !validEvalID(id) {
		fmt.Fprintf(os.Stderr, "bad agent id %q\n%s\n", pos[0], usage)
		return exitUsage
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()

	var v any
	if strings.TrimSpace(c.server) != "" {
		cl, err := newSyncClient(&syncOpts{server: c.server, token: c.token, org: c.org})
		if err != nil {
			return fail("%v", err)
		}
		h := &httpEval{c: cl}
		p := "/agents/" + urlPathEscape(id) + "/versions"
		if sub == "versions" {
			var vs agentVersions
			if err := h.call(ctx, http.MethodGet, p, nil, &vs); err != nil {
				return fail("%v", err)
			}
			v = &vs
		} else {
			var row agentVersion
			if err := h.call(ctx, http.MethodPost, p, map[string]string{"note": note}, &row); err != nil {
				return fail("%v", err)
			}
			v = &row
		}
	} else {
		root, err := projectRoot(projectConfig(c.config, c.configSet))
		if err != nil {
			return fail("%v", err)
		}
		if sub == "versions" {
			vs, err := localAgentVersions(root, id)
			if err != nil {
				return fail("%v", err)
			}
			v = vs
		} else {
			row, _, err := localAgentSnapshot(root, id, note, os.Getenv("USER"), time.Now().UTC())
			if err != nil {
				return fail("%v", err)
			}
			v = row
		}
	}
	if *asJSON {
		return writeJSON(out, v)
	}
	switch x := v.(type) {
	case *agentVersions:
		renderAgentVersions(out, x)
	case *agentVersion:
		fmt.Fprintf(out, "%s v%d  %s\n", id, x.Version, short(x.Hash))
	}
	return exitOK
}

func renderAgentVersions(w io.Writer, vs *agentVersions) {
	if len(vs.Versions) == 0 {
		fmt.Fprintf(w, "%s has no saved versions yet; save one with: %s agent snapshot %s -note \"...\"\n", vs.Agent, progName, vs.Agent)
		return
	}
	tw := tabwriter.NewWriter(w, 0, 2, 2, ' ', 0)
	fmt.Fprintln(tw, "VERSION\tHASH\tCREATED\tBY\tRUNS\tNOTE")
	for i := len(vs.Versions) - 1; i >= 0; i-- {
		r := vs.Versions[i]
		fmt.Fprintf(tw, "v%d\t%s\t%s\t%s\t%d\t%s\n", r.Version, short(r.Hash), r.CreatedAt.Local().Format("2006-01-02 15:04"), r.CreatedBy, r.Runs, r.Note)
	}
	tw.Flush()
}

func agentVersionsDir(root, id string) string {
	return filepath.Join(heimdallDir(root), "agent-versions", id)
}

func localAgentVersions(root, id string) (*agentVersions, error) {
	out := &agentVersions{Agent: id, Versions: []agentVersion{}}
	raw, err := os.ReadFile(filepath.Join(agentVersionsDir(root, id), "versions.json"))
	if errors.Is(err, os.ErrNotExist) {
		return out, nil
	}
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal(raw, &out.Versions); err != nil {
		return nil, fmt.Errorf("versions of %s: %w", id, err)
	}
	return out, nil
}

// localAgentSnapshot freezes agents/<id>.yaml as the next version. The same
// config hash as the newest version makes no new version (created=false).
func localAgentSnapshot(root, id, note, by string, now time.Time) (*agentVersion, bool, error) {
	cfg := filepath.Join(root, "agents", id+".yaml")
	raw, err := os.ReadFile(cfg)
	if err != nil {
		return nil, false, fmt.Errorf("read agent config: %w", err)
	}
	sum := sha256.Sum256(raw)
	hash := hex.EncodeToString(sum[:])
	vs, err := localAgentVersions(root, id)
	if err != nil {
		return nil, false, err
	}
	if n := len(vs.Versions); n > 0 && vs.Versions[n-1].Hash == hash {
		last := vs.Versions[n-1]
		return &last, false, nil
	}
	next := 1
	if n := len(vs.Versions); n > 0 {
		next = vs.Versions[n-1].Version + 1
	}
	dir := agentVersionsDir(root, id)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, false, err
	}
	if err := os.WriteFile(filepath.Join(dir, fmt.Sprintf("v%d.yaml", next)), raw, 0o644); err != nil {
		return nil, false, err
	}
	row := agentVersion{Version: next, Hash: hash, CreatedAt: now, CreatedBy: by, Note: note}
	vs.Versions = append(vs.Versions, row)
	idx, err := json.MarshalIndent(vs.Versions, "", "  ")
	if err != nil {
		return nil, false, err
	}
	tmp := filepath.Join(dir, "versions.json.tmp")
	if err := os.WriteFile(tmp, append(idx, '\n'), 0o644); err != nil {
		return nil, false, err
	}
	if err := os.Rename(tmp, filepath.Join(dir, "versions.json")); err != nil {
		return nil, false, err
	}
	return &row, true, nil
}
