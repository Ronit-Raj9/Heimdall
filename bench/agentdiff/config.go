package agentdiff

import (
	"bytes"
	"encoding/json"
	"fmt"
	"path"
	"reflect"
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/maximhq/bifrost/bench/utils"
)

// redacted replaces a secret value on both sides of a change.
const redacted = "[redacted]"

// secretNameParts are the env name fragments that mark a credential. They are
// the planner's rule for the environment policy hash (engine/planner.go), so
// the diff hides exactly what the manifest identity hides.
var secretNameParts = []string{"KEY", "TOKEN", "SECRET", "PASSWORD", "AUTH"}

// identityFields are the canonical arm identity fields the resolved config
// does not already carry; the rest of the identity repeats config fields.
var identityFields = []string{"adapter_version", "agent_version", "image_digest", "parser_version"}

// armSide is one version's resolved arm, as the config diff reads it.
type armSide struct {
	arm       schemas.ArmManifest
	condition string
}

// DiffConfigs compares two resolved arms field by field and file by file.
// Fields are the dotted paths of the config's JSON form plus the manifest's
// image ids, budget snapshot and the identity fields the config lacks. Env
// values whose name marks a credential are redacted, and every string value
// is passed through utils.RedactSecrets. Files are the hashes the planner laid
// down for condition (clean, or any condition when the arm has none for it);
// task-keyed files ("<task>:<path>") are compared only for tasks both arms
// list, so a different task selection is not reported as a config change.
// A changed file gets a unified diff when both contents are at hand: the
// config's own file templates, or the notes user file read through contents
// when it still hashes as planned. It is pure apart from contents.
func DiffConfigs(a, b schemas.ArmManifest, condition string, contents ContentSource) (ConfigDiff, error) {
	fa, err := flattenArm(a)
	if err != nil {
		return ConfigDiff{}, fmt.Errorf("config of version a: %w", err)
	}
	fb, err := flattenArm(b)
	if err != nil {
		return ConfigDiff{}, fmt.Errorf("config of version b: %w", err)
	}
	byGroup := map[string]*ConfigGroup{}
	group := func(id string) *ConfigGroup {
		g, ok := byGroup[id]
		if !ok {
			g = &ConfigGroup{ID: id, Fields: []FieldChange{}, Files: []FileChange{}}
			byGroup[id] = g
		}
		return g
	}
	for _, c := range diffFields(fa, fb) {
		g := group(fieldGroup(c.Path))
		g.Fields = append(g.Fields, c)
	}
	for _, f := range diffFiles(armSide{a, condition}, armSide{b, condition}, contents) {
		g := group(fileGroup(f.Path, a.Config, b.Config))
		g.Files = append(g.Files, f)
	}
	out := ConfigDiff{Groups: []ConfigGroup{}}
	for _, id := range GroupOrder {
		if g, ok := byGroup[id]; ok {
			out.Groups = append(out.Groups, *g)
			out.Changes += len(g.Fields) + len(g.Files)
		}
	}
	out.Identical = out.Changes == 0
	return out, nil
}

// flattenArm turns an arm into dotted path -> leaf. Files are left out (they
// are compared by hash), env values are redacted by name, and empty leaves
// (null, "", {}, []) are dropped so an unset field and an empty one compare
// equal.
func flattenArm(a schemas.ArmManifest) (map[string]any, error) {
	cfg := a.Config
	cfg.Files = nil
	out, err := flattenJSON(cfg)
	if err != nil {
		return nil, err
	}
	for k := range out {
		if name, ok := strings.CutPrefix(k, "env."); ok && secretName(name) {
			out[k] = secretValue{v: out[k]}
		}
	}
	extra := map[string]any{"image_id": a.ImageID, "task_images": a.TaskImages, "task_image_ids": a.TaskImageIDs, "budget_snapshot": a.BudgetSnapshot}
	if a.Identity != nil {
		id, err := flattenJSON(a.Identity)
		if err != nil {
			return nil, err
		}
		for _, f := range identityFields {
			extra["identity."+f] = id[f]
		}
	}
	more, err := flattenJSON(extra)
	if err != nil {
		return nil, err
	}
	for k, v := range more {
		out[k] = v
	}
	return out, nil
}

// secretValue keeps a secret's identity for comparison without its text:
// equal values stay equal, different ones differ, and neither leaves the
// process. The value shown is always redacted.
type secretValue struct{ v any }

// diffFields returns the changed paths of two flat maps, sorted by path.
func diffFields(a, b map[string]any) []FieldChange {
	paths := make([]string, 0, len(a)+len(b))
	for p := range a {
		paths = append(paths, p)
	}
	for p := range b {
		if _, ok := a[p]; !ok {
			paths = append(paths, p)
		}
	}
	slices.Sort(paths)
	var out []FieldChange
	for _, p := range paths {
		va, inA := a[p]
		vb, inB := b[p]
		if inA && inB && reflect.DeepEqual(va, vb) {
			continue
		}
		c := FieldChange{Path: p, Kind: KindChanged}
		switch {
		case !inA:
			c.Kind = KindAdded
		case !inB:
			c.Kind = KindRemoved
		}
		var ra, rb bool
		c.Old, ra = shown(va, inA)
		c.New, rb = shown(vb, inB)
		c.Redacted = ra || rb
		out = append(out, c)
	}
	return out
}

// shown is the value a change displays: nil when absent, "[redacted]" for a
// secret, and every string with utils.RedactSecrets applied. The bool says
// whether anything was hidden.
func shown(v any, present bool) (any, bool) {
	if !present {
		return nil, false
	}
	if _, ok := v.(secretValue); ok {
		return redacted, true
	}
	return redactLeaf(v)
}

// redactLeaf applies utils.RedactSecrets to every string in a leaf.
func redactLeaf(v any) (any, bool) {
	switch x := v.(type) {
	case string:
		r := utils.RedactSecrets(x)
		return r, r != x
	case []any:
		out := make([]any, len(x))
		hid := false
		for i, e := range x {
			var h bool
			out[i], h = redactLeaf(e)
			hid = hid || h
		}
		return out, hid
	case map[string]any:
		out := make(map[string]any, len(x))
		hid := false
		for k, e := range x {
			var h bool
			out[k], h = redactLeaf(e)
			hid = hid || h
		}
		return out, hid
	default:
		return v, false
	}
}

// secretName reports whether an env name marks a credential.
func secretName(name string) bool {
	u := strings.ToUpper(name)
	for _, p := range secretNameParts {
		if strings.Contains(u, p) {
			return true
		}
	}
	return false
}

// flattenJSON turns a value's JSON form into dotted path -> leaf. Objects are
// walked; arrays and scalars are leaves; empty leaves are dropped.
func flattenJSON(v any) (map[string]any, error) {
	data, err := json.Marshal(v)
	if err != nil {
		return nil, fmt.Errorf("encode: %w", err)
	}
	var tree any
	if err := json.Unmarshal(data, &tree); err != nil {
		return nil, fmt.Errorf("decode: %w", err)
	}
	out := map[string]any{}
	flattenInto(out, "", tree)
	return out, nil
}

// flattenInto writes the non-empty leaves of tree under prefix.
func flattenInto(out map[string]any, prefix string, tree any) {
	switch x := tree.(type) {
	case nil:
		return
	case string:
		if x == "" {
			return
		}
	case []any:
		if len(x) == 0 {
			return
		}
	case map[string]any:
		for k, v := range x {
			p := k
			if prefix != "" {
				p = prefix + "." + k
			}
			flattenInto(out, p, v)
		}
		return
	}
	if prefix != "" {
		out[prefix] = tree
	}
}

// fieldGroup sorts a dotted path into its config group.
func fieldGroup(p string) string {
	head, rest, _ := strings.Cut(p, ".")
	switch head {
	case "model":
		return GroupModel
	case "notes":
		return GroupPrompt
	case "mcp", "skills", "memory", "capabilities":
		return GroupTools
	case "budgets", "budget_snapshot":
		return GroupBudgets
	case "gateway":
		return GroupGateway
	case "image", "build", "image_digest", "image_id", "task_images", "task_image_ids", "agent_version":
		return GroupImage
	case "identity":
		if rest == "image_digest" || rest == "agent_version" {
			return GroupImage
		}
	case "env":
		return envGroup(rest)
	}
	return GroupOther
}

// envGroup sorts an env variable by its name: a model, prompt or tool
// setting passed through the environment belongs with its kind.
func envGroup(name string) string {
	u := strings.ToUpper(name)
	switch {
	case strings.Contains(u, "MODEL"):
		return GroupModel
	case strings.Contains(u, "PROMPT"), strings.Contains(u, "INSTRUCTION"), strings.Contains(u, "NOTES"):
		return GroupPrompt
	case strings.Contains(u, "MCP"), strings.Contains(u, "TOOL"), strings.Contains(u, "SKILL"):
		return GroupTools
	}
	return GroupOther
}

// fileGroup sorts a laid-down file into its config group: notes and
// markdown or prompt files are prompt and notes, skills and MCP config are
// tools, anything else is other.
func fileGroup(key string, a, b schemas.AgentConfig) string {
	p := key
	if _, rest, ok := strings.Cut(key, ":"); ok {
		p = rest
	}
	lower := strings.ToLower(p)
	for _, c := range []schemas.AgentConfig{a, b} {
		if c.Notes.UserPath != "" && p == path.Clean(c.Notes.UserPath) {
			return GroupPrompt
		}
		if c.Notes.ProjectFile != "" && path.Base(p) == path.Base(c.Notes.ProjectFile) {
			return GroupPrompt
		}
		if c.MCP != nil && c.MCP.ConfigPath != "" && p == path.Clean(c.MCP.ConfigPath) {
			return GroupTools
		}
		if c.Skills != nil && c.Skills.Dir != "" && strings.HasPrefix(p, path.Clean(c.Skills.Dir)+"/") {
			return GroupTools
		}
	}
	switch {
	case strings.Contains(lower, "/skills/"), strings.Contains(lower, "mcp"):
		return GroupTools
	case strings.HasSuffix(lower, ".md"), strings.Contains(lower, "prompt"), strings.Contains(lower, "instruction"):
		return GroupPrompt
	}
	return GroupOther
}

// diffFiles compares the two arms' laid-down file hashes.
func diffFiles(a, b armSide, contents ContentSource) []FileChange {
	ha, hb := filesFor(a), filesFor(b)
	tasksA, tasksB := fileTasks(ha), fileTasks(hb)
	keep := func(k string) bool {
		task, _, ok := strings.Cut(k, ":")
		return !ok || (tasksA[task] && tasksB[task])
	}
	keys := make([]string, 0, len(ha)+len(hb))
	for k := range ha {
		keys = append(keys, k)
	}
	for k := range hb {
		if _, ok := ha[k]; !ok {
			keys = append(keys, k)
		}
	}
	slices.Sort(keys)
	var out []FileChange
	for _, k := range keys {
		if !keep(k) {
			continue
		}
		oldHash, inA := ha[k]
		newHash, inB := hb[k]
		if inA && inB && oldHash == newHash {
			continue
		}
		f := FileChange{Path: k, Kind: KindChanged, OldHash: oldHash, NewHash: newHash}
		switch {
		case !inA:
			f.Kind = KindAdded
		case !inB:
			f.Kind = KindRemoved
		}
		textDiff(&f, fileContent(a.arm, k, oldHash, contents), fileContent(b.arm, k, newHash, contents))
		out = append(out, f)
	}
	return out
}

// filesFor returns the arm's file hashes for the condition, falling back to
// clean and then to the first condition by name.
func filesFor(s armSide) map[string]string {
	if f, ok := s.arm.Files[s.condition]; ok {
		return f
	}
	if f, ok := s.arm.Files[schemas.ConditionClean]; ok {
		return f
	}
	conds := make([]string, 0, len(s.arm.Files))
	for c := range s.arm.Files {
		conds = append(conds, c)
	}
	slices.Sort(conds)
	if len(conds) == 0 {
		return nil
	}
	return s.arm.Files[conds[0]]
}

// fileTasks returns the task ids of a file map's task-keyed entries.
func fileTasks(files map[string]string) map[string]bool {
	out := map[string]bool{}
	for k := range files {
		if task, _, ok := strings.Cut(k, ":"); ok {
			out[task] = true
		}
	}
	return out
}

// content is one side of a file as the text diff sees it.
type content struct {
	text string
	have bool   // the text is at hand and matches the planned hash
	why  string // why it is not, when have is false
}

// fileContent finds a file's text: the config's own template for an arm
// file, or the notes user file through contents. A side that is absent has
// empty text. The text must hash to the planned hash, so a file edited on
// disk after the plan is never shown as the planned one.
func fileContent(a schemas.ArmManifest, key, hash string, contents ContentSource) content {
	if hash == "" {
		return content{have: true}
	}
	var data []byte
	found := false
	for dst, tmpl := range a.Config.Files {
		if path.Clean(dst) == key {
			data, found = []byte(tmpl), true
			break
		}
	}
	if !found && a.Config.Notes.UserFile != "" && key == path.Clean(a.Config.Notes.UserPath) && contents != nil {
		data, found = contents(a.Config.Notes.UserFile)
		if found && utils.SHA256Bytes(data) != hash {
			return content{why: "the file on disk changed since the plan; compared by hash only"}
		}
	}
	switch {
	case !found:
		return content{why: "content not on disk; compared by hash only"}
	case len(data) > MaxFileBytes:
		return content{why: fmt.Sprintf("larger than %d KiB; compared by hash only", MaxFileBytes>>10)}
	case bytes.IndexByte(data, 0) >= 0 || !utf8.Valid(data):
		return content{why: "binary; compared by hash only"}
	}
	return content{text: utils.RedactSecrets(string(data)), have: true}
}

// textDiff fills a file change's diff from both sides' contents.
func textDiff(f *FileChange, a, b content) {
	switch {
	case !a.have:
		f.DiffNote = a.why
		return
	case !b.have:
		f.DiffNote = b.why
		return
	}
	d, truncated, ok := unifiedDiff(f.Path, a.text, b.text, MaxDiffLines)
	switch {
	case !ok:
		f.DiffNote = "too many lines for a text diff; compared by hash only"
	case d == "":
		f.DiffNote = "the hashes differ only in redacted secrets"
	default:
		f.Diff, f.Truncated = d, truncated
	}
}
