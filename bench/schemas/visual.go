package schemas

// X1 vision grading (docs/visual-grading.md): what a task's work looks like,
// graded on images the bench captured itself. Every type here is optional
// on the task (OutputSpec.Visual) and on the grade (Grade.Visual), so specs,
// manifests, locks and seals written before it keep their hashes.

// Capture kinds (VisualCapture.Kind): where the graded image comes from.
// The bench takes every capture after the agent exited; the agent never
// hands one in.
const (
	VisualCaptureBrowser  = "browser"  // the bench opens Capture.URL in the run's browser service and screenshots it
	VisualCaptureDesktop  = "desktop"  // the bench calls the run's desktop service's own screenshot tool
	VisualCaptureArtifact = "artifact" // an artifact file: png and jpg as they are, svg and html rendered in a clean room
)

// Check methods (VisualCheck.Method, VisualAssert.Method).
const (
	VisualMethodDimensions = "dimensions" // the capture has the expected width and height
	VisualMethodPixel      = "pixel"      // share of pixels that differ from the reference by more than Tolerance in a channel
	VisualMethodSSIM       = "ssim"       // mean structural similarity of the luma, 8x8 windows
	VisualMethodPHash      = "phash"      // Hamming distance of 64-bit DCT perceptual hashes
	VisualMethodColors     = "colors"     // each expected colour is a dominant colour of the area
	VisualMethodRegion     = "region"     // a crop of the reference is found in the capture, optionally near a point
)

// VisualCheck is the visual part of a task's output (output.visual): what
// the bench captures after the session, the hidden reference it is compared
// with, the deterministic checks, and an optional rubric for the vision
// judge. Method and Threshold name the first check, made over the whole
// image against Reference; Checks add more. A grade passes only when every
// check passes. The judge never decides alone: without a calibration record
// for its model and prompt it reports and does not decide.
type VisualCheck struct {
	Capture   VisualCapture `json:"capture" yaml:"capture"`
	Reference string        `json:"reference,omitempty" yaml:"reference"` // hidden reference image (png), relative to the task folder
	Method    string        `json:"method,omitempty" yaml:"method"`
	Threshold float64       `json:"threshold,omitempty" yaml:"threshold"` // method's pass threshold; 0 takes the method's default
	Tolerance int           `json:"tolerance,omitempty" yaml:"tolerance"` // pixel: per-channel difference ignored, 0 to 255
	// Masks are regions every comparison ignores (a clock, a cursor, a
	// timestamp), in capture pixels.
	Masks  []VisualRect     `json:"masks,omitempty" yaml:"masks"`
	Checks []VisualAssert   `json:"checks,omitempty" yaml:"checks"`
	Rubric string           `json:"rubric,omitempty" yaml:"rubric"` // judge rubric file, relative to the task folder
	Judge  *VisualJudgeSpec `json:"judge,omitempty" yaml:"judge"`   // pinned vision judge; required with Rubric
}

// VisualCapture says what the bench captures and how, so the same state
// gives the same pixels: a fixed viewport, device scale factor and the
// font set of a pinned image.
type VisualCapture struct {
	Kind     string `json:"kind" yaml:"kind"`                     // browser | desktop | artifact
	Service  string `json:"service,omitempty" yaml:"service"`     // browser, desktop: the service id; default browser or desktop
	URL      string `json:"url,omitempty" yaml:"url"`             // browser: the page opened, e.g. http://webapp:8000/
	Artifact string `json:"artifact,omitempty" yaml:"artifact"`   // artifact: the ArtifactSpec id
	File     string `json:"file,omitempty" yaml:"file"`           // artifact: the file's base name; default the spec's only file
	Width    int    `json:"width,omitempty" yaml:"width"`         // browser, svg, html: viewport width; default 1280
	Height   int    `json:"height,omitempty" yaml:"height"`       // browser, svg, html: viewport height; default 800
	Scale    int    `json:"scale,omitempty" yaml:"scale"`         // browser, svg, html: device scale factor 1 to 3; default 1
	FullPage bool   `json:"full_page,omitempty" yaml:"full_page"` // browser, html: the whole page, not only the viewport
	WaitMS   int    `json:"wait_ms,omitempty" yaml:"wait_ms"`     // settle time after load, before the shot; default 0, at most 10000
	// Image is the renderer image for svg and html artifacts; default the
	// pinned Playwright image the browser tasks use.
	Image string `json:"image,omitempty" yaml:"image"`
}

// VisualRect is a rectangle in image pixels; X and Y are its top-left
// corner.
type VisualRect struct {
	X int `json:"x" yaml:"x"`
	Y int `json:"y" yaml:"y"`
	W int `json:"w" yaml:"w"`
	H int `json:"h" yaml:"h"`
}

// VisualPoint is a position in image pixels.
type VisualPoint struct {
	X int `json:"x" yaml:"x"`
	Y int `json:"y" yaml:"y"`
}

// VisualAssert is one more deterministic check.
type VisualAssert struct {
	ID        string      `json:"id,omitempty" yaml:"id"` // name in the grade; default <method>_<n>
	Method    string      `json:"method" yaml:"method"`
	Threshold float64     `json:"threshold,omitempty" yaml:"threshold"`
	Tolerance int         `json:"tolerance,omitempty" yaml:"tolerance"` // pixel
	Area      *VisualRect `json:"area,omitempty" yaml:"area"`           // pixel, ssim, phash, colors: compare only this rectangle; region: search only here
	// Colors: the colours (#rrggbb) that must each be a dominant colour of
	// the area, within Threshold (RGB distance, default 24), and cover at
	// least MinShare of it (default 0.01).
	Colors   []string `json:"colors,omitempty" yaml:"colors"`
	MinShare float64  `json:"min_share,omitempty" yaml:"min_share"`
	// Region: the crop to find, a rectangle of the reference image (Crop)
	// or an image file of the task folder (CropFile); At is where its
	// top-left corner must be found, within MaxOffset pixels (default 2).
	Crop      *VisualRect  `json:"crop,omitempty" yaml:"crop"`
	CropFile  string       `json:"crop_file,omitempty" yaml:"crop_file"`
	At        *VisualPoint `json:"at,omitempty" yaml:"at"`
	MaxOffset int          `json:"max_offset,omitempty" yaml:"max_offset"`
	// Dimensions: the expected size; default the reference's.
	Width  int `json:"width,omitempty" yaml:"width"`
	Height int `json:"height,omitempty" yaml:"height"`
}

// VisualJudgeSpec pins the vision judge like the text judge (JudgeSpec):
// model, prompt version and votes are in the task file, so the suite lock
// fingerprints them. Aggregate majority passes on a majority of pass
// verdicts; mean passes when the mean score reaches PassScore.
type VisualJudgeSpec struct {
	Provider      string  `json:"provider" yaml:"provider"`
	Model         string  `json:"model" yaml:"model"`
	PromptVersion string  `json:"prompt_version" yaml:"prompt_version"` // names the embedded prompt, e.g. visual-v1
	Votes         int     `json:"votes,omitempty" yaml:"votes"`         // samples; odd for majority; default 1
	Aggregate     string  `json:"aggregate,omitempty" yaml:"aggregate"` // majority (default) | mean
	PassScore     float64 `json:"pass_score,omitempty" yaml:"pass_score"`
	MaxTokens     int     `json:"max_tokens,omitempty" yaml:"max_tokens"`
}

// VisualGrade is what a visual grade rested on (Grade.Visual): the captured
// image and how it was taken, the reference and the diff heatmap kept in
// the run folder, every check's number, and the judge's report.
type VisualGrade struct {
	Capture   VisualImage         `json:"capture"`
	Source    VisualSource        `json:"source"`
	Reference *VisualImage        `json:"reference,omitempty"`
	Diff      *VisualImage        `json:"diff,omitempty"` // heatmap: red where pixels differ, blue over masks
	Masks     []VisualRect        `json:"masks,omitempty"`
	Checks    []VisualCheckResult `json:"checks,omitempty"`
	Passed    int                 `json:"passed"` // checks passed
	Score     *float64            `json:"score,omitempty"`
	Judge     *VisualJudgeResult  `json:"judge,omitempty"`
}

// VisualImage is one image file of the run folder.
type VisualImage struct {
	Path   string `json:"path"` // relative to the run folder
	SHA256 string `json:"sha256"`
	Bytes  int64  `json:"bytes"`
	Width  int    `json:"width"`
	Height int    `json:"height"`
}

// VisualSource is how the capture was taken.
type VisualSource struct {
	Kind     string `json:"kind"`
	Service  string `json:"service,omitempty"`
	URL      string `json:"url,omitempty"`
	Artifact string `json:"artifact,omitempty"` // artifacts/<id>/<name>
	Renderer string `json:"renderer,omitempty"` // image the svg or html was rendered with
	Width    int    `json:"width,omitempty"`
	Height   int    `json:"height,omitempty"`
	Scale    int    `json:"scale,omitempty"`
	FullPage bool   `json:"full_page,omitempty"`
	Fonts    string `json:"fonts_sha256,omitempty"` // sha256 of the renderer's font list
	Format   string `json:"format"`                 // png | jpeg as decoded

	PointerParked bool `json:"pointer_parked,omitempty"` // desktop: the pointer was moved to the bottom-right corner before the shot
}

// VisualCheckResult is one check's number. Value's meaning follows the
// method: share of differing pixels (pixel), mean SSIM (ssim), Hamming
// distance (phash), the largest distance of an expected colour (colors), the
// mean absolute difference per channel at the best match, 0 to 1 (region),
// and 1 or 0 (dimensions).
type VisualCheckResult struct {
	ID        string       `json:"id"`
	Method    string       `json:"method"`
	Value     float64      `json:"value"`
	Threshold float64      `json:"threshold"`
	Passed    bool         `json:"passed"`
	Detail    string       `json:"detail,omitempty"`
	Area      *VisualRect  `json:"area,omitempty"`
	Found     *VisualPoint `json:"found,omitempty"` // region: where the best match is
}

// VisualJudgeResult is the vision judge's report. Decides is false until a
// calibration record for the judge's model and prompt exists; then the
// grade is judged and the record's agreement numbers are kept beside it.
type VisualJudgeResult struct {
	Model         string            `json:"model"`
	ServedModel   string            `json:"served_model,omitempty"`
	PromptVersion string            `json:"prompt_version"`
	PromptSHA256  string            `json:"prompt_sha256"`
	RubricSHA256  string            `json:"rubric_sha256"`
	Aggregate     string            `json:"aggregate"`
	Votes         []string          `json:"votes"`  // pass | fail | unparsable
	Scores        []float64         `json:"scores"` // each sample's score, 0 to 1; unparsable samples are left out
	Score         *float64          `json:"score,omitempty"`
	Verdict       string            `json:"verdict"` // pass | fail | undecided
	Rationale     string            `json:"rationale,omitempty"`
	USD           Value             `json:"usd"` // never added to the agent's cost
	PriceRef      string            `json:"price_ref,omitempty"`
	Calibration   string            `json:"calibration,omitempty"` // "<record id>@<hash>"; empty when none
	Decides       bool              `json:"decides"`
	Reason        string            `json:"reason,omitempty"` // why it does not decide
	Agreement     *CalibrationStats `json:"agreement,omitempty"`
}
