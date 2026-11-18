package codescan

import (
	"encoding/json"
	"fmt"
	"net/url"
	"regexp"
	"strconv"
	"strings"

	"github.com/maximhq/bifrost/bench/schemas"
)

// SARIF 2.1.0 (OASIS), only the fields the scan reads. Scanners differ in
// where they put a rule's CWE and severity; every place known to be used is
// read (see cweOf and severityOf).
type sarifLog struct {
	Version string     `json:"version"`
	Runs    []sarifRun `json:"runs"`
}

type sarifRun struct {
	Tool    sarifTool     `json:"tool"`
	Results []sarifResult `json:"results"`
}

type sarifTool struct {
	Driver sarifDriver `json:"driver"`
}

type sarifDriver struct {
	Name  string      `json:"name"`
	Rules []sarifRule `json:"rules"`
}

type sarifRule struct {
	ID                   string          `json:"id"`
	Name                 string          `json:"name"`
	ShortDescription     sarifText       `json:"shortDescription"`
	DefaultConfiguration sarifConfig     `json:"defaultConfiguration"`
	Properties           sarifProperties `json:"properties"`
	Relationships        []struct {
		Target struct {
			ID            string `json:"id"`
			ToolComponent struct {
				Name string `json:"name"`
			} `json:"toolComponent"`
		} `json:"target"`
	} `json:"relationships"`
}

type sarifConfig struct {
	Level string `json:"level"`
}

type sarifText struct {
	Text string `json:"text"`
}

// sarifProperties is a property bag: tags, a GitHub-style
// security-severity and the scanner's own fields (bandit's issue_severity,
// a cwe object or string).
type sarifProperties map[string]any

type sarifResult struct {
	RuleID    string          `json:"ruleId"`
	RuleIndex *int            `json:"ruleIndex"`
	Level     string          `json:"level"`
	Message   sarifText       `json:"message"`
	Locations []sarifLocation `json:"locations"`
	Taxa      []struct {
		ID            string `json:"id"`
		ToolComponent struct {
			Name string `json:"name"`
		} `json:"toolComponent"`
	} `json:"taxa"`
	Properties sarifProperties `json:"properties"`
}

type sarifLocation struct {
	PhysicalLocation struct {
		ArtifactLocation struct {
			URI string `json:"uri"`
		} `json:"artifactLocation"`
		Region struct {
			StartLine int `json:"startLine"`
			EndLine   int `json:"endLine"`
		} `json:"region"`
	} `json:"physicalLocation"`
}

// addedLine is one line the agent added, as the SARIF filter sees it.
type addedLine struct {
	Patch int
	Text  string
}

// AddedLines indexes a diff's added lines by post-run path and line number.
func AddedLines(diff []byte) (map[string]map[int]addedLine, error) {
	files, err := parseDiff(diff)
	if err != nil {
		return nil, err
	}
	out := map[string]map[int]addedLine{}
	for _, f := range files {
		if f.Binary || f.Status == "deleted" {
			continue
		}
		for _, h := range f.Hunks {
			for _, l := range h.Lines {
				if l.Kind != kindAdded {
					continue
				}
				m := out[f.path()]
				if m == nil {
					m = map[int]addedLine{}
					out[f.path()] = m
				}
				m[l.New] = addedLine{Patch: l.Patch, Text: l.Text}
			}
		}
	}
	return out, nil
}

// ParseSARIF reads a scanner's SARIF 2.1.0 log and keeps the results that
// fall on a line the agent added: a result whose region [startLine,
// endLine] holds an added line of the same file is reported on its first
// such line. It returns those findings and the number of results in the log.
// workdir is the folder the scanner saw the workspace in (/workspace by
// default), stripped from absolute URIs. The snippet is the added line from
// the diff, redacted; nothing the scanner printed is kept beyond the rule's
// title, also redacted.
func ParseSARIF(scanner string, data []byte, added map[string]map[int]addedLine, workdir string) ([]schemas.CodeScanFinding, int, error) {
	var log sarifLog
	if err := json.Unmarshal(data, &log); err != nil {
		return nil, 0, fmt.Errorf("SARIF does not parse: %w", err)
	}
	if log.Version != "2.1.0" {
		return nil, 0, fmt.Errorf("SARIF version %q, want 2.1.0", log.Version)
	}
	total := 0
	var out []schemas.CodeScanFinding
	for _, run := range log.Runs {
		byID := map[string]sarifRule{}
		for _, r := range run.Tool.Driver.Rules {
			byID[r.ID] = r
		}
		for _, res := range run.Results {
			total++
			rule := byID[res.RuleID]
			if res.RuleIndex != nil && *res.RuleIndex >= 0 && *res.RuleIndex < len(run.Tool.Driver.Rules) && res.RuleID == "" {
				rule = run.Tool.Driver.Rules[*res.RuleIndex]
			}
			id := res.RuleID
			if id == "" {
				id = rule.ID
			}
			for _, loc := range res.Locations {
				file := sarifPath(loc.PhysicalLocation.ArtifactLocation.URI, workdir)
				start, end := loc.PhysicalLocation.Region.StartLine, loc.PhysicalLocation.Region.EndLine
				if end < start {
					end = start
				}
				line, ok := firstAdded(added[file], start, end)
				if !ok {
					continue
				}
				title := rule.ShortDescription.Text
				if title == "" {
					title = rule.Name
				}
				if title == "" {
					title = res.Message.Text
				}
				out = append(out, schemas.CodeScanFinding{
					Rule: scanner + ":" + id, Title: Redact(file, clip(title, 120)), CWE: cweOf(rule, res), Severity: severityOf(rule, res),
					File: file, Line: line, PatchLine: added[file][line].Patch, Snippet: Redact(file, added[file][line].Text),
					Source: scanner, InTest: isTestPath(file),
				})
				break
			}
		}
	}
	sortFindings(out)
	return out, total, nil
}

// sarifPath turns an artifact URI into a workspace-relative path.
func sarifPath(uri, workdir string) string {
	if u, err := url.PathUnescape(uri); err == nil {
		uri = u
	}
	uri = strings.TrimPrefix(uri, "file://")
	if workdir == "" {
		workdir = "/workspace"
	}
	uri = strings.TrimPrefix(uri, strings.TrimSuffix(workdir, "/")+"/")
	return strings.TrimPrefix(uri, "./")
}

// firstAdded returns the first added line in [start, end].
func firstAdded(lines map[int]addedLine, start, end int) (int, bool) {
	if start <= 0 {
		return 0, false
	}
	for n := start; n <= end && n < start+200; n++ {
		if _, ok := lines[n]; ok {
			return n, true
		}
	}
	return 0, false
}

var cweRE = regexp.MustCompile(`(?i)\bcwe[-_/ ]?([0-9]{1,5})\b`)

// cweOf finds the CWE a result names: result taxa or rule relationships with
// a CWE tool component (gosec), a cwe property ({"id": 78} or "CWE-78",
// bandit), or a tag such as "external/cwe/cwe-78" (CodeQL) or "CWE-78:
// ..." (semgrep).
func cweOf(rule sarifRule, res sarifResult) string {
	for _, t := range res.Taxa {
		if strings.EqualFold(t.ToolComponent.Name, "CWE") && t.ID != "" {
			return "CWE-" + strings.TrimPrefix(strings.ToUpper(t.ID), "CWE-")
		}
	}
	for _, r := range rule.Relationships {
		if strings.EqualFold(r.Target.ToolComponent.Name, "CWE") && r.Target.ID != "" {
			return "CWE-" + strings.TrimPrefix(strings.ToUpper(r.Target.ID), "CWE-")
		}
	}
	for _, props := range []sarifProperties{res.Properties, rule.Properties} {
		switch v := props["cwe"].(type) {
		case map[string]any:
			if id, ok := v["id"]; ok {
				return "CWE-" + strings.TrimPrefix(strings.ToUpper(fmt.Sprint(id)), "CWE-")
			}
		case string:
			if m := cweRE.FindStringSubmatch(v); m != nil {
				return "CWE-" + m[1]
			}
		case float64:
			return "CWE-" + strconv.Itoa(int(v))
		}
		if tags, ok := props["tags"].([]any); ok {
			for _, tag := range tags {
				if m := cweRE.FindStringSubmatch(fmt.Sprint(tag)); m != nil {
					return "CWE-" + m[1]
				}
			}
		}
	}
	return ""
}

// severityOf maps a result to a code scan severity: a numeric
// security-severity (9.0 and up critical, 7.0 high, 4.0 medium, else low),
// else a scanner's own issue_severity or severity property, else the SARIF
// level (error high, warning medium, note and none low; SARIF's default
// level is warning).
func severityOf(rule sarifRule, res sarifResult) string {
	for _, props := range []sarifProperties{res.Properties, rule.Properties} {
		if s, ok := props["security-severity"]; ok {
			if f, err := strconv.ParseFloat(fmt.Sprint(s), 64); err == nil {
				switch {
				case f >= 9:
					return schemas.CodeScanCritical
				case f >= 7:
					return schemas.CodeScanHigh
				case f >= 4:
					return schemas.CodeScanMedium
				default:
					return schemas.CodeScanLow
				}
			}
		}
		for _, key := range []string{"issue_severity", "severity"} {
			if s, ok := props[key].(string); ok {
				if v := strings.ToLower(s); schemas.CodeScanSeverityRank(v) > 0 {
					return v
				}
			}
		}
	}
	level := res.Level
	if level == "" {
		level = rule.DefaultConfiguration.Level
	}
	switch level {
	case "error":
		return schemas.CodeScanHigh
	case "note", "none":
		return schemas.CodeScanLow
	default:
		return schemas.CodeScanMedium
	}
}
