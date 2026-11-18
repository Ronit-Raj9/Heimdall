package admitscan

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/maximhq/bifrost/bench/schemas"
)

// AdmissionsRootEnv overrides where records are stored.
const AdmissionsRootEnv = "BENCH_ADMISSIONS_ROOT"

// AdmissionsDir is the folder under the bench root that holds records.
const AdmissionsDir = "admissions"

var recordIDRe = regexp.MustCompile(`^adm_[a-z0-9]{4,32}$`)

// AdmissionsRoot is $BENCH_ADMISSIONS_ROOT when set, else
// <benchRoot>/admissions. It returns "" when neither is known.
func AdmissionsRoot(benchRoot string) string {
	if v := strings.TrimSpace(os.Getenv(AdmissionsRootEnv)); v != "" {
		return v
	}
	if benchRoot == "" {
		return ""
	}
	return filepath.Join(benchRoot, AdmissionsDir)
}

// StoreRecord writes <root>/<id>/admission.json and admission.md and
// returns the folder. An existing record folder is never overwritten.
func StoreRecord(root string, rec *schemas.AdmitScanRecord) (string, error) {
	if !recordIDRe.MatchString(rec.ID) {
		return "", fmt.Errorf("store admission record: bad id %q", rec.ID)
	}
	dir := filepath.Join(root, rec.ID)
	if err := os.MkdirAll(root, 0o755); err != nil {
		return "", fmt.Errorf("create admissions root: %w", err)
	}
	if err := os.Mkdir(dir, 0o755); err != nil {
		return "", fmt.Errorf("create admission folder %s: %w", rec.ID, err)
	}
	if err := WriteJSON(filepath.Join(dir, schemas.AdmitScanRecordFile), rec); err != nil {
		return "", err
	}
	if err := os.WriteFile(filepath.Join(dir, schemas.AdmitScanReportFile), RenderMarkdown(rec), 0o644); err != nil {
		return "", fmt.Errorf("write admission report: %w", err)
	}
	return dir, nil
}

// WriteJSON writes the record as indented JSON.
func WriteJSON(path string, rec *schemas.AdmitScanRecord) error {
	b, err := MarshalRecord(rec)
	if err != nil {
		return err
	}
	if err := os.WriteFile(path, b, 0o644); err != nil {
		return fmt.Errorf("write admission record: %w", err)
	}
	return nil
}

// MarshalRecord is the record as indented JSON with a trailing newline.
func MarshalRecord(rec *schemas.AdmitScanRecord) ([]byte, error) {
	b, err := json.MarshalIndent(rec, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("encode admission record: %w", err)
	}
	return append(b, '\n'), nil
}

// ReadRecord reads an admission.json.
func ReadRecord(path string) (*schemas.AdmitScanRecord, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read admission record: %w", err)
	}
	var rec schemas.AdmitScanRecord
	if err := json.Unmarshal(b, &rec); err != nil {
		return nil, fmt.Errorf("decode admission record %s: %w", path, err)
	}
	return &rec, nil
}

// RenderMarkdown is the human report of a record.
func RenderMarkdown(rec *schemas.AdmitScanRecord) []byte {
	var b strings.Builder
	fmt.Fprintf(&b, "# Admission %s: %s %s\n\n", rec.ID, rec.Kind, mdEscape(rec.Subject))
	fmt.Fprintf(&b, "**Decision: %s**\n\n", rec.Decision)
	fmt.Fprintf(&b, "| | |\n|---|---|\n")
	fmt.Fprintf(&b, "| Source | `%s` |\n", mdCode(rec.Source))
	fmt.Fprintf(&b, "| Content hash | `%s` |\n", rec.ContentHash)
	fmt.Fprintf(&b, "| Scanned | %s, scanner %s |\n", rec.CreatedAt.Format("2006-01-02 15:04:05Z07:00"), rec.ScannerVersion)
	fmt.Fprintf(&b, "| Findings | %d block, %d warn, %d info |\n", rec.Counts.Block, rec.Counts.Warn, rec.Counts.Info)
	allow := "none"
	if len(rec.AllowHosts) > 0 {
		allow = strings.Join(rec.AllowHosts, ", ")
	}
	fmt.Fprintf(&b, "| Allowed hosts | %s |\n", mdEscape(allow))
	if rec.Override != nil {
		fmt.Fprintf(&b, "| Override | %s (by %s at %s) |\n", mdEscape(rec.Override.Reason), mdEscape(orUnknown(rec.Override.By)), rec.Override.At.Format("2006-01-02 15:04:05Z07:00"))
	}
	b.WriteString("\n## Findings\n\n")
	if len(rec.Findings) == 0 {
		b.WriteString("None.\n")
	} else {
		b.WriteString("| Severity | Rule | Where | Detail | Excerpt |\n|---|---|---|---|---|\n")
		for _, f := range rec.Findings {
			detail := mdEscape(f.Detail)
			if f.Decoded != "" {
				detail += "<br>decoded: `" + mdCode(clip(Visible(f.Decoded), 200)) + "`"
			}
			fmt.Fprintf(&b, "| %s | %s | %s | %s | %s |\n", f.Severity, f.Rule, mdEscape(where(f)), detail, codeOrEmpty(f.Excerpt))
		}
	}
	renderDynamic(&b, rec.Dynamic)
	renderMCP(&b, rec.MCP)
	renderFiles(&b, rec.Files)
	b.WriteString("\n## What this scan does not cover\n\n")
	for _, l := range rec.Limits {
		fmt.Fprintf(&b, "- %s\n", l)
	}
	return []byte(b.String())
}

func renderDynamic(b *strings.Builder, d *schemas.AdmitScanDynamic) {
	if d == nil {
		return
	}
	b.WriteString("\n## Sandbox run\n\n")
	if !d.Ran {
		b.WriteString("Nothing ran.\n")
		return
	}
	fmt.Fprintf(b, "- Image `%s`, helper `%s`, %d ms", mdCode(d.Image), mdCode(d.HelperImage), d.DurationMS)
	if d.ExitCode != nil {
		fmt.Fprintf(b, ", exit %d", *d.ExitCode)
	}
	if d.TimedOut {
		b.WriteString(", killed at the wall budget")
	}
	b.WriteString("\n")
	fmt.Fprintf(b, "- Watcher observed: %t; canary file or folder opens: %d; network attempts: %d; output %d bytes\n", d.Observed, d.FileEvents, d.NetEvents, d.OutputBytes)
	if d.Error != "" {
		fmt.Fprintf(b, "- Watcher error: %s\n", mdEscape(d.Error))
	}
	for _, c := range d.Commands {
		fmt.Fprintf(b, "- Ran `%s`\n", mdCode(c))
	}
	for _, c := range d.Canaries {
		fmt.Fprintf(b, "- Canary %s at `%s` (value sha256 %s)\n", c.ID, mdCode(c.Path), short12(c.SHA256))
	}
}

func renderMCP(b *strings.Builder, m *schemas.AdmitScanMCP) {
	if m == nil {
		return
	}
	b.WriteString("\n## MCP server\n\n")
	fmt.Fprintf(b, "- Server %s %s, protocol %s, endpoint `%s`\n", mdEscape(orUnknown(m.ServerName)), mdEscape(m.ServerVersion), mdEscape(orUnknown(m.ProtocolVersion)), mdCode(m.Endpoint))
	fmt.Fprintf(b, "- tools/list compared %d times; probe call: %s; list_changed sent: %t\n", m.Listings, mdEscape(orUnknown(m.ProbeCall)), m.ListChanged)
	for _, t := range m.Tools {
		fmt.Fprintf(b, "- Tool `%s` sha256 %s\n", mdCode(t.Name), short12(t.SHA256))
	}
	if len(m.Prompts) > 0 {
		fmt.Fprintf(b, "- Prompts: %s\n", mdEscape(strings.Join(m.Prompts, ", ")))
	}
	if len(m.Resources) > 0 {
		fmt.Fprintf(b, "- Resources: %s\n", mdEscape(strings.Join(m.Resources, ", ")))
	}
}

func renderFiles(b *strings.Builder, files []schemas.AdmitScanFile) {
	if len(files) == 0 {
		return
	}
	b.WriteString("\n## Files\n\n| Path | Size | sha256 | Note |\n|---|---|---|---|\n")
	for _, f := range files {
		note := f.Note
		if f.Binary && note == "" {
			note = "binary, not scanned"
		}
		h := f.SHA256
		if f.Symlink != "" {
			h = "link to " + Visible(f.Symlink)
		}
		fmt.Fprintf(b, "| `%s` | %d | %s | %s |\n", mdCode(Visible(f.Path)), f.Size, mdEscape(h), mdEscape(note))
	}
}

func where(f schemas.AdmitScanFinding) string {
	switch {
	case f.Line > 0:
		return fmt.Sprintf("%s:%d:%d", Visible(f.File), f.Line, f.Column)
	default:
		return Visible(f.File)
	}
}

func orUnknown(s string) string {
	if s == "" {
		return "unknown"
	}
	return s
}

func codeOrEmpty(s string) string {
	if s == "" {
		return ""
	}
	return "`" + mdCode(s) + "`"
}

// mdEscape keeps table cells intact.
func mdEscape(s string) string {
	s = strings.ReplaceAll(s, "|", `\|`)
	s = strings.ReplaceAll(s, "<", "&lt;")
	s = strings.ReplaceAll(s, "\n", " ")
	return strings.ReplaceAll(s, "\r", " ")
}

// mdCode keeps code spans intact: backticks and pipes would end the span or
// the cell.
func mdCode(s string) string {
	s = strings.ReplaceAll(s, "`", "'")
	s = strings.ReplaceAll(s, "|", `\|`)
	return strings.NewReplacer("\n", " ", "\r", " ").Replace(s)
}
