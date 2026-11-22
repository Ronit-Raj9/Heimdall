package mcptest

import (
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strings"

	"github.com/maximhq/bifrost/bench/admitscan"
)

// Issue is one quality problem of a tool or of the whole server.
type Issue struct {
	Code     string `json:"code"`
	Severity string `json:"severity"` // warn | info
	Field    string `json:"field,omitempty"`
	Message  string `json:"message"`
	// Fix says what to change, in the server author's terms.
	Fix string `json:"fix"`
}

// Issue severities.
const (
	SevWarn = "warn"
	SevInfo = "info"
)

// ToolQuality grades one tool's name, description and input schema.
type ToolQuality struct {
	Tool             string  `json:"tool"`
	DescriptionScore int     `json:"description_score"` // 0 to 100
	SchemaScore      int     `json:"schema_score"`      // 0 to 100
	Params           int     `json:"params"`
	Required         int     `json:"required"`
	ReadOnly         bool    `json:"read_only"`   // annotations.readOnlyHint
	Destructive      bool    `json:"destructive"` // annotations.destructiveHint
	Issues           []Issue `json:"issues"`
}

// QualityReport is the schema and description quality of a whole listing.
type QualityReport struct {
	SchemaScore      int           `json:"schema_score"`
	DescriptionScore int           `json:"description_score"`
	Tools            []ToolQuality `json:"tools"`
	Server           []Issue       `json:"server_issues"`
}

// Quality limits.
const (
	minDescriptionChars = 20
	minDescriptionWords = 4
	maxDescriptionChars = 1024
	maxParams           = 15
	maxSchemaDepth      = 4
	manyTools           = 40
)

var toolNameRe = regexp.MustCompile(`^[A-Za-z0-9_.-]{1,64}$`)

// vagueNames are tool or parameter names that say nothing on their own.
var vagueNames = map[string]bool{
	"run": true, "do": true, "execute": true, "process": true, "handle": true, "tool": true, "call": true, "action": true,
	"query": true, "get": true, "data": true, "input": true, "value": true, "params": true, "args": true, "x": true,
	"q": true, "obj": true, "payload": true, "info": true, "thing": true, "item": true, "body": true, "request": true,
}

// returnWords are words that say what a tool gives back.
var returnWords = regexp.MustCompile(`(?i)\b(returns?|lists?|gives?|outputs?|results?|responds?|yields?|fetch(es)?|retrieves?|reads?|shows?|creates?|updates?|deletes?|sends?|writes?)\b`)

// schemaNode is the part of a JSON Schema the checks read.
type schemaNode struct {
	Type                 any                    `json:"type"`
	Description          string                 `json:"description"`
	Properties           map[string]*schemaNode `json:"properties"`
	Required             []string               `json:"required"`
	Items                *schemaNode            `json:"items"`
	Enum                 []any                  `json:"enum"`
	AnyOf                []*schemaNode          `json:"anyOf"`
	OneOf                []*schemaNode          `json:"oneOf"`
	Ref                  string                 `json:"$ref"`
	AdditionalProperties any                    `json:"additionalProperties"`
	Default              any                    `json:"default"`
	Examples             []any                  `json:"examples"`
}

func (n *schemaNode) typeName() string {
	switch t := n.Type.(type) {
	case string:
		return t
	case []any:
		for _, x := range t {
			if s, ok := x.(string); ok && s != "null" {
				return s
			}
		}
	}
	return ""
}

func (n *schemaNode) typed() bool {
	return n.typeName() != "" || len(n.Enum) > 0 || len(n.AnyOf) > 0 || len(n.OneOf) > 0 || n.Ref != ""
}

func depth(n *schemaNode) int {
	if n == nil {
		return 0
	}
	d := 0
	for _, p := range n.Properties {
		d = max(d, depth(p))
	}
	if n.Items != nil {
		d = max(d, depth(n.Items))
	}
	if len(n.Properties) > 0 || n.Items != nil {
		return d + 1
	}
	return d
}

// annotations reads the behaviour hints of a tool.
func annotations(t admitscan.MCPTool) (readOnly, destructive bool) {
	var r struct {
		Annotations struct {
			ReadOnly    *bool `json:"readOnlyHint"`
			Destructive *bool `json:"destructiveHint"`
		} `json:"annotations"`
	}
	_ = json.Unmarshal(t.Raw, &r)
	if r.Annotations.ReadOnly != nil {
		readOnly = *r.Annotations.ReadOnly
	}
	if r.Annotations.Destructive != nil {
		destructive = *r.Annotations.Destructive
	}
	return readOnly, destructive
}

// GradeTool grades one tool. It is pure.
func GradeTool(t admitscan.MCPTool) ToolQuality {
	q := ToolQuality{Tool: t.Name, Issues: []Issue{}}
	q.ReadOnly, q.Destructive = annotations(t)
	desc, sch := 100, 100
	add := func(score *int, cost int, is Issue) {
		*score -= cost
		q.Issues = append(q.Issues, is)
	}

	// Name.
	if !toolNameRe.MatchString(t.Name) {
		add(&desc, 10, Issue{Code: "name_invalid", Severity: SevWarn, Field: "name", Message: "The name has characters or a length many model APIs refuse.",
			Fix: "Use 1 to 64 letters, digits, '_', '-' or '.'."})
	}
	if vagueNames[strings.ToLower(t.Name)] {
		add(&desc, 10, Issue{Code: "name_vague", Severity: SevWarn, Field: "name", Message: fmt.Sprintf("The name %q does not say what the tool acts on.", t.Name),
			Fix: "Name the action and the object, for example search_orders."})
	}

	// Description.
	d := strings.TrimSpace(t.Description)
	words := len(strings.Fields(d))
	switch {
	case d == "":
		add(&desc, 45, Issue{Code: "description_missing", Severity: SevWarn, Field: "description", Message: "The tool has no description, so an agent picks it from the name alone.",
			Fix: "Say what the tool does, when to use it and what it returns."})
	case len(d) < minDescriptionChars || words < minDescriptionWords:
		add(&desc, 25, Issue{Code: "description_short", Severity: SevWarn, Field: "description", Message: fmt.Sprintf("The description is %d words long.", words),
			Fix: "Say what the tool does, when to use it and what it returns, in one or two sentences."})
	case strings.EqualFold(strings.Trim(d, ". "), humanize(t.Name)):
		add(&desc, 20, Issue{Code: "description_repeats_name", Severity: SevWarn, Field: "description", Message: "The description only repeats the name.",
			Fix: "Add when to use the tool and what it returns."})
	}
	if len(d) > maxDescriptionChars {
		add(&desc, 10, Issue{Code: "description_long", Severity: SevInfo, Field: "description", Message: fmt.Sprintf("The description is %d characters; long descriptions crowd the agent's context on every turn.", len(d)),
			Fix: "Keep the description under 1,000 characters and move detail into parameter descriptions."})
	}
	if d != "" && !returnWords.MatchString(d) {
		add(&desc, 10, Issue{Code: "description_no_result", Severity: SevInfo, Field: "description", Message: "The description does not say what the tool returns or changes.",
			Fix: "Add what the tool gives back, for example 'Returns the matching orders with id and status.'"})
	}

	// Input schema.
	raw := strings.TrimSpace(string(t.InputSchema))
	var root schemaNode
	switch {
	case raw == "" || raw == "null":
		add(&sch, 60, Issue{Code: "schema_missing", Severity: SevWarn, Field: "inputSchema", Message: "The tool has no input schema; MCP requires one.",
			Fix: `Send {"type":"object","properties":{...}} even when the tool takes no input.`})
	case json.Unmarshal(t.InputSchema, &root) != nil:
		add(&sch, 60, Issue{Code: "schema_invalid", Severity: SevWarn, Field: "inputSchema", Message: "The input schema is not a JSON object.",
			Fix: "Send a JSON Schema object."})
	default:
		if root.typeName() != "object" {
			add(&sch, 30, Issue{Code: "schema_not_object", Severity: SevWarn, Field: "inputSchema.type", Message: "The input schema's type is not object.",
				Fix: `Set "type": "object"; tool arguments are always an object.`})
		}
		q.Params, q.Required = len(root.Properties), len(root.Required)
		sch -= paramIssues(&root, &q)
		if depth(&root) > maxSchemaDepth {
			add(&sch, 10, Issue{Code: "schema_deep", Severity: SevInfo, Field: "inputSchema", Message: fmt.Sprintf("The input nests deeper than %d levels; agents often get deep objects wrong.", maxSchemaDepth),
				Fix: "Flatten the input or split the tool."})
		}
		if len(root.Properties) > maxParams {
			add(&sch, 10, Issue{Code: "schema_many_params", Severity: SevInfo, Field: "inputSchema.properties", Message: fmt.Sprintf("The tool takes %d parameters.", len(root.Properties)),
				Fix: "Split the tool, or group rarely used options."})
		}
	}
	q.DescriptionScore, q.SchemaScore = max(desc, 0), max(sch, 0)
	return q
}

// paramIssues adds the per-parameter issues and returns the schema points
// they cost: 40 points spread over the parameters for missing descriptions,
// 30 for missing types, 15 for each required name that is not a property.
func paramIssues(root *schemaNode, q *ToolQuality) int {
	names := make([]string, 0, len(root.Properties))
	for n := range root.Properties {
		names = append(names, n)
	}
	sort.Strings(names)
	var noDesc, noType, vague []string
	for _, n := range names {
		p := root.Properties[n]
		if p == nil {
			p = &schemaNode{}
		}
		if strings.TrimSpace(p.Description) == "" {
			noDesc = append(noDesc, n)
		}
		if !p.typed() {
			noType = append(noType, n)
		}
		if vagueNames[strings.ToLower(n)] && len(strings.Fields(p.Description)) < minDescriptionWords {
			vague = append(vague, n)
		}
	}
	cost := 0
	if len(names) > 0 && len(noDesc) > 0 {
		cost += 40 * len(noDesc) / len(names)
		q.Issues = append(q.Issues, Issue{Code: "param_no_description", Severity: SevWarn, Field: "inputSchema.properties",
			Message: fmt.Sprintf("%d of %d parameters have no description: %s.", len(noDesc), len(names), strings.Join(noDesc, ", ")),
			Fix:     "Describe each parameter: its meaning, format and an example value."})
	}
	if len(names) > 0 && len(noType) > 0 {
		cost += 30 * len(noType) / len(names)
		q.Issues = append(q.Issues, Issue{Code: "param_no_type", Severity: SevWarn, Field: "inputSchema.properties",
			Message: fmt.Sprintf("%d parameters have no type: %s.", len(noType), strings.Join(noType, ", ")),
			Fix:     "Give every parameter a type (string, integer, number, boolean, array or object), or an enum."})
	}
	if len(vague) > 0 {
		cost += 10
		q.Issues = append(q.Issues, Issue{Code: "param_ambiguous", Severity: SevWarn, Field: "inputSchema.properties",
			Message: fmt.Sprintf("Ambiguous parameter names with little or no description: %s.", strings.Join(vague, ", ")),
			Fix:     "Rename them after what they hold (customer_id, search_text) or describe them fully."})
	}
	for _, r := range root.Required {
		if _, ok := root.Properties[r]; !ok {
			cost += 15
			q.Issues = append(q.Issues, Issue{Code: "required_unknown", Severity: SevWarn, Field: "inputSchema.required",
				Message: fmt.Sprintf("%q is required but is not a property.", r),
				Fix:     "List only declared properties under required."})
		}
	}
	return cost
}

// GradeListing grades every tool and the listing as a whole. It is pure.
func GradeListing(tools []admitscan.MCPTool) QualityReport {
	r := QualityReport{Tools: make([]ToolQuality, 0, len(tools)), Server: []Issue{}}
	if len(tools) == 0 {
		r.Server = append(r.Server, Issue{Code: "no_tools", Severity: SevWarn, Message: "The server lists no tools.", Fix: "Expose at least one tool, or check that the credential has access."})
		return r
	}
	sd, ss := 0, 0
	byDesc := map[string][]string{}
	byNorm := map[string][]string{}
	for _, t := range tools {
		q := GradeTool(t)
		r.Tools = append(r.Tools, q)
		sd += q.DescriptionScore
		ss += q.SchemaScore
		if d := strings.ToLower(strings.TrimSpace(t.Description)); d != "" {
			byDesc[d] = append(byDesc[d], t.Name)
		}
		byNorm[normName(t.Name)] = append(byNorm[normName(t.Name)], t.Name)
	}
	r.DescriptionScore, r.SchemaScore = sd/len(tools), ss/len(tools)
	for _, names := range sortedGroups(byDesc) {
		r.Server = append(r.Server, Issue{Code: "descriptions_identical", Severity: SevWarn, Field: "description",
			Message: "These tools share one description, so an agent cannot tell them apart: " + strings.Join(names, ", ") + ".",
			Fix:     "Say in each description how the tool differs from the others."})
	}
	for _, names := range sortedGroups(byNorm) {
		r.Server = append(r.Server, Issue{Code: "names_collide", Severity: SevWarn, Field: "name",
			Message: "These names differ only in case or separators: " + strings.Join(names, ", ") + ".",
			Fix:     "Give each tool a distinct name."})
	}
	if len(tools) > manyTools {
		r.Server = append(r.Server, Issue{Code: "many_tools", Severity: SevInfo,
			Message: fmt.Sprintf("The server lists %d tools; agents pick worse among many similar tools.", len(tools)),
			Fix:     "Group tools by task, or offer a smaller default set."})
	}
	return r
}

func sortedGroups(m map[string][]string) [][]string {
	var out [][]string
	for _, names := range m {
		if len(names) > 1 {
			sort.Strings(names)
			out = append(out, names)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i][0] < out[j][0] })
	return out
}

func normName(s string) string {
	return strings.NewReplacer("_", "", "-", "", ".", "", " ", "").Replace(strings.ToLower(s))
}

// humanize turns get_order_status into "get order status".
func humanize(name string) string {
	return strings.TrimSpace(strings.NewReplacer("_", " ", "-", " ", ".", " ").Replace(strings.ToLower(name)))
}
