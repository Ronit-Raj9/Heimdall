package schemas

import "time"

// This file holds the admission record of a skill or MCP server scan
// (docs/admission-scan.md, `bifrost-bench skill scan` and `bifrost-bench mcp
// scan`). A record is a child artifact outside every seal: it says what was
// scanned (with the content hash of every file), what the scan found, and
// whether the subject was admitted. A changed skill hashes differently and
// needs a new record.

// Admission kinds (AdmitScanRecord.Kind).
const (
	AdmitScanKindSkill = "skill"
	AdmitScanKindMCP   = "mcp"
)

// Admission decisions (AdmitScanRecord.Decision).
const (
	AdmitScanAdmitted             = "admitted"
	AdmitScanBlocked              = "blocked"
	AdmitScanAdmittedWithOverride = "admitted_with_override"
)

// Finding severities (AdmitScanFinding.Severity). One block finding blocks
// admission unless an override reason is given; warn and info never do.
const (
	AdmitScanSeverityBlock = "block"
	AdmitScanSeverityWarn  = "warn"
	AdmitScanSeverityInfo  = "info"
)

// AdmitScanRecordFile and AdmitScanReportFile are the files of one record
// under <admissions root>/<id>/.
const (
	AdmitScanRecordFile = "admission.json"
	AdmitScanReportFile = "admission.md"
)

// AdmitScanRecord is the outcome of one admission scan.
type AdmitScanRecord struct {
	ID             string             `json:"id"`
	Kind           string             `json:"kind"`    // skill | mcp
	Subject        string             `json:"subject"` // skill folder name or MCP service id
	Source         string             `json:"source"`  // the folder or service config scanned
	CreatedAt      time.Time          `json:"created_at"`
	ScannerVersion string             `json:"scanner_version"`
	Decision       string             `json:"decision"` // admitted | blocked | admitted_with_override
	Override       *AdmitScanOverride `json:"override,omitempty"`
	ContentHash    string             `json:"content_hash"` // sha256 over every file's path and hash (skill) or the listings and service spec (mcp)
	AllowHosts     []string           `json:"allow_hosts"`
	Counts         AdmitScanCounts    `json:"counts"`
	Files          []AdmitScanFile    `json:"files,omitempty"`
	Findings       []AdmitScanFinding `json:"findings"`
	Dynamic        *AdmitScanDynamic  `json:"dynamic,omitempty"`
	MCP            *AdmitScanMCP      `json:"mcp,omitempty"`
	Limits         []string           `json:"limits"` // what this scan cannot see, repeated in every report
}

// AdmitScanOverride records who admitted a blocked subject anyway, and why.
type AdmitScanOverride struct {
	Reason string    `json:"reason"`
	By     string    `json:"by,omitempty"` // the local user name when known
	At     time.Time `json:"at"`
}

// AdmitScanCounts counts findings by severity.
type AdmitScanCounts struct {
	Block int `json:"block"`
	Warn  int `json:"warn"`
	Info  int `json:"info"`
}

// AdmitScanFile is one file of a scanned skill folder.
type AdmitScanFile struct {
	Path    string `json:"path"` // slash-separated, relative to the skill folder
	SHA256  string `json:"sha256,omitempty"`
	Size    int64  `json:"size"`
	Binary  bool   `json:"binary,omitempty"`
	Symlink string `json:"symlink,omitempty"` // the link target; links are never followed
	Scanned bool   `json:"scanned"`           // false for binaries, links, special files and files over the cap
	Note    string `json:"note,omitempty"`
}

// AdmitScanFinding is one thing the scan found. Excerpt shows invisible and
// control characters as <U+XXXX>; Decoded holds a decoded payload (tag
// characters, base64, hex) when there is one.
type AdmitScanFinding struct {
	Rule     string `json:"rule"`
	Severity string `json:"severity"` // block | warn | info
	File     string `json:"file"`     // relative path, or mcp:<server>/<where> for MCP text
	Line     int    `json:"line,omitempty"`
	Column   int    `json:"column,omitempty"`
	Excerpt  string `json:"excerpt,omitempty"`
	Decoded  string `json:"decoded,omitempty"`
	Detail   string `json:"detail"`
}

// AdmitScanDynamic is what the sandboxed run of a skill's setup scripts, or
// of an MCP server, showed.
type AdmitScanDynamic struct {
	Ran         bool              `json:"ran"`
	Image       string            `json:"image"`
	HelperImage string            `json:"helper_image"`
	Commands    []string          `json:"commands,omitempty"` // what ran, in order
	ExitCode    *int              `json:"exit_code,omitempty"`
	TimedOut    bool              `json:"timed_out,omitempty"`
	DurationMS  int64             `json:"duration_ms"`
	Canaries    []AdmitScanCanary `json:"canaries"`
	FileEvents  int               `json:"file_events"` // canary file and folder opens seen
	NetEvents   int               `json:"net_events"`  // network attempts seen
	Observed    bool              `json:"observed"`    // false when the watcher could not run; the run then proves nothing
	Error       string            `json:"error,omitempty"`
	OutputBytes int64             `json:"output_bytes"`
}

// AdmitScanCanary is one planted fake credential. The value itself is never
// recorded, only its hash.
type AdmitScanCanary struct {
	ID     string `json:"id"`   // ssh_key | aws | gh | netrc | dotenv
	Path   string `json:"path"` // container path
	SHA256 string `json:"value_sha256"`
}

// AdmitScanMCP is what the MCP server said about itself.
type AdmitScanMCP struct {
	ServerName      string             `json:"server_name,omitempty"`
	ServerVersion   string             `json:"server_version,omitempty"`
	ProtocolVersion string             `json:"protocol_version,omitempty"`
	Endpoint        string             `json:"endpoint"`
	Tools           []AdmitScanMCPTool `json:"tools"`
	Prompts         []string           `json:"prompts,omitempty"`
	Resources       []string           `json:"resources,omitempty"`
	Listings        int                `json:"listings"` // tools/list calls compared for the rug-pull check
	ProbeCall       string             `json:"probe_call,omitempty"`
	ListChanged     bool               `json:"list_changed,omitempty"` // the server sent notifications/tools/list_changed
}

// AdmitScanMCPTool pins one tool as first listed.
type AdmitScanMCPTool struct {
	Name   string `json:"name"`
	SHA256 string `json:"sha256"` // over the tool's canonical JSON (name, description, schemas, annotations)
}
