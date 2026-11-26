package improve

import (
	"fmt"
	"maps"
	"regexp"
	"slices"
	"strings"

	"github.com/maximhq/bifrost/bench/utils"
	"gopkg.in/yaml.v3"
)

// Bundle is the agent's editable content: container path to file text.
type Bundle map[string]string

// Hash is the bundle's content hash; the empty bundle has a fixed hash.
func (b Bundle) Hash() string {
	var sb strings.Builder
	for _, k := range slices.Sorted(maps.Keys(b)) {
		fmt.Fprintf(&sb, "%d:%s\x00%d:%s\x00", len(k), k, len(b[k]), b[k])
	}
	return utils.SHA256Bytes([]byte(sb.String()))
}

// Clone copies the bundle.
func (b Bundle) Clone() Bundle { return maps.Clone(b) }

// ExtractBundle reads the editable content out of an agent YAML document:
// editable paths of files and files_raw, and the standing instructions as
// InstructionsKey when they are editable (an empty text when unset).
func ExtractBundle(doc []byte, ed Editable) (Bundle, error) {
	tree, err := parseTree(doc)
	if err != nil {
		return nil, err
	}
	out := Bundle{}
	for _, field := range []string{"files", "files_raw"} {
		m, _ := tree[field].(map[string]any)
		for k, v := range m {
			if !ed.Allows(k) {
				continue
			}
			s, ok := v.(string)
			if !ok {
				return nil, fmt.Errorf("agent file %s is not text", k)
			}
			out[k] = s
		}
	}
	if ed.Instructions {
		s, ok := tree["instructions"].(string)
		if !ok && tree["instructions"] != nil {
			return nil, fmt.Errorf("agent instructions are not text")
		}
		out[InstructionsKey] = s
	}
	return out, nil
}

// templatePlaceholder matches a {{name}} placeholder as utils.Render does.
var templatePlaceholder = regexp.MustCompile(`\{\{\s*([a-zA-Z0-9_.\-]+)\s*\}\}`)

// placeholderCounts counts the template placeholders of a text by name.
func placeholderCounts(s string) map[string]int {
	out := map[string]int{}
	for _, m := range templatePlaceholder.FindAllStringSubmatch(s, -1) {
		out[m[1]]++
	}
	return out
}

// newPlaceholder is the first placeholder name whose count rises from old
// to cur, or "".
func newPlaceholder(old, cur string) string {
	before, after := placeholderCounts(old), placeholderCounts(cur)
	for _, name := range slices.Sorted(maps.Keys(after)) {
		if after[name] > before[name] {
			return name
		}
	}
	return ""
}

// ApplyBundle writes b over the editable content of an agent YAML
// document. Each path goes back to the map it came from in the parent: a
// path of files stays templated, a path of files_raw stays raw, a new path
// goes to files_raw, and InstructionsKey goes to instructions. Editable
// paths of either map not in b are removed, every other key is kept as it
// is. A path outside the editable set is refused, so a bundle can never
// reach a frozen field, and a templated file may not gain a placeholder.
func ApplyBundle(doc []byte, ed Editable, b Bundle) ([]byte, error) {
	for k := range b {
		if !ed.Allows(k) {
			return nil, fmt.Errorf("bundle path %s is not editable", k)
		}
	}
	tree, err := parseTree(doc)
	if err != nil {
		return nil, err
	}
	curFiles, _ := tree["files"].(map[string]any)
	curRaw, _ := tree["files_raw"].(map[string]any)
	files, raw := map[string]any{}, map[string]any{}
	for k, v := range curFiles {
		if !ed.Allows(k) {
			files[k] = v
		}
	}
	for k, v := range curRaw {
		if !ed.Allows(k) {
			raw[k] = v
		}
	}
	if ed.Instructions {
		delete(tree, "instructions")
	}
	for _, k := range slices.Sorted(maps.Keys(b)) {
		v := b[k]
		switch old, templated := curFiles[k]; {
		case k == InstructionsKey:
			if v != "" {
				tree["instructions"] = v
			}
		case templated:
			oldText, _ := old.(string)
			if name := newPlaceholder(oldText, v); name != "" {
				return nil, fmt.Errorf("%s would gain the template placeholder {{%s}}; write it in a new file instead", k, name)
			}
			files[k] = v
		default:
			raw[k] = v
		}
	}
	for field, m := range map[string]map[string]any{"files": files, "files_raw": raw} {
		if len(m) == 0 {
			delete(tree, field)
		} else {
			tree[field] = m
		}
	}
	out, err := yaml.Marshal(tree)
	if err != nil {
		return nil, fmt.Errorf("encode agent config: %w", err)
	}
	return out, nil
}

// BundleObjectOf is the bundle object (A1.2) of a version's bundle, with
// the paths its document delivers raw (files_raw) listed in Raw.
func BundleObjectOf(doc []byte, b Bundle) (BundleObject, error) {
	tree, err := parseTree(doc)
	if err != nil {
		return BundleObject{}, err
	}
	raw, _ := tree["files_raw"].(map[string]any)
	obj := BundleObject{Files: maps.Clone(map[string]string(b)), Raw: []string{}}
	if obj.Files == nil {
		obj.Files = map[string]string{}
	}
	for _, k := range slices.Sorted(maps.Keys(b)) {
		if _, ok := raw[k]; ok {
			obj.Raw = append(obj.Raw, k)
		}
	}
	return obj, nil
}

func parseTree(doc []byte) (map[string]any, error) {
	var tree map[string]any
	if err := yaml.Unmarshal(doc, &tree); err != nil {
		return nil, fmt.Errorf("parse agent config: %w", err)
	}
	if tree == nil {
		tree = map[string]any{}
	}
	return tree, nil
}

// FileDiff is one bundle file's change from the parent to the candidate.
type FileDiff struct {
	Path   string     `json:"path"`
	Status string     `json:"status"` // added | removed | changed
	Lines  []DiffLine `json:"lines"`
	Added  int        `json:"added"`
	Gone   int        `json:"removed"`
}

// DiffLine is one line of a file diff: Op is "+", "-" or " ".
type DiffLine struct {
	Op   string `json:"op"`
	Text string `json:"text"`
}

// maxDiffLines caps the lines compared per file; larger files are shown as
// whole-file replacements.
const maxDiffLines = 2000

// Diff returns the changed files of b against parent, sorted by path.
func Diff(parent, b Bundle) []FileDiff {
	out := []FileDiff{}
	paths := map[string]bool{}
	for k := range parent {
		paths[k] = true
	}
	for k := range b {
		paths[k] = true
	}
	for _, p := range slices.Sorted(maps.Keys(paths)) {
		old, hadOld := parent[p]
		cur, hasCur := b[p]
		if hadOld && hasCur && old == cur {
			continue
		}
		fd := FileDiff{Path: p, Status: "changed"}
		switch {
		case !hadOld:
			fd.Status = "added"
		case !hasCur:
			fd.Status = "removed"
		}
		fd.Lines = lineDiff(splitLines(old), splitLines(cur))
		for _, l := range fd.Lines {
			switch l.Op {
			case "+":
				fd.Added++
			case "-":
				fd.Gone++
			}
		}
		out = append(out, fd)
	}
	return out
}

// AddedText is the text of every added line across the diff: what the
// improver wrote, which the integrity gates read.
func AddedText(d []FileDiff) string {
	var sb strings.Builder
	for _, f := range d {
		for _, l := range f.Lines {
			if l.Op == "+" {
				sb.WriteString(l.Text)
				sb.WriteByte('\n')
			}
		}
	}
	return sb.String()
}

func splitLines(s string) []string {
	if s == "" {
		return nil
	}
	return strings.Split(strings.TrimSuffix(s, "\n"), "\n")
}

// lineDiff is a longest-common-subsequence line diff.
func lineDiff(a, b []string) []DiffLine {
	if len(a) > maxDiffLines || len(b) > maxDiffLines {
		out := make([]DiffLine, 0, len(a)+len(b))
		for _, l := range a {
			out = append(out, DiffLine{Op: "-", Text: l})
		}
		for _, l := range b {
			out = append(out, DiffLine{Op: "+", Text: l})
		}
		return out
	}
	n, m := len(a), len(b)
	lcs := make([][]int, n+1)
	for i := range lcs {
		lcs[i] = make([]int, m+1)
	}
	for i := n - 1; i >= 0; i-- {
		for j := m - 1; j >= 0; j-- {
			if a[i] == b[j] {
				lcs[i][j] = lcs[i+1][j+1] + 1
			} else {
				lcs[i][j] = max(lcs[i+1][j], lcs[i][j+1])
			}
		}
	}
	out := []DiffLine{}
	i, j := 0, 0
	for i < n && j < m {
		switch {
		case a[i] == b[j]:
			out = append(out, DiffLine{Op: " ", Text: a[i]})
			i++
			j++
		case lcs[i+1][j] >= lcs[i][j+1]:
			out = append(out, DiffLine{Op: "-", Text: a[i]})
			i++
		default:
			out = append(out, DiffLine{Op: "+", Text: b[j]})
			j++
		}
	}
	for ; i < n; i++ {
		out = append(out, DiffLine{Op: "-", Text: a[i]})
	}
	for ; j < m; j++ {
		out = append(out, DiffLine{Op: "+", Text: b[j]})
	}
	return out
}
