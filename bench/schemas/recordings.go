package schemas

import (
	"context"
	"time"
)

// Recordings, live view and human takeover of browser and desktop runs
// (lane Z03).
//
// A service may declare recordings: files it writes while the run goes
// (a browser video and HAR from Playwright, a desktop video from ffmpeg,
// console messages). After the agent exits and the state exports are read,
// the workspace runs the service's stop command, waits for the service to
// finish its files, and copies them into the run folder at
// RecordingsDir/<service>/<file> with one RecordingRecord per file in
// RecordingsDir/RecordingsIndexFile. They are sealed and hashed with the
// rest of the run folder, but they are never mounted for the verifier: the
// grade reads only state/ and the answer. Recordings are display evidence.
//
// A service may also declare live: a command that prints one screen image
// (PNG or JPEG) to stdout. While the agent runs, the engine samples it
// every LiveFrameEvery and publishes the newest frame for the run page; the
// frames are display only and are never written to the run folder. In a
// debug run (Experiment.Debug) a person may take control: each input goes
// to the service's input command, is written to HumanActionsFile in the
// run folder and marks the run human_assisted, which keeps it out of every
// verdict, report card and leaderboard.

// RecordingsDir is the recordings folder in the run folder.
const RecordingsDir = "recordings"

// RecordingsIndexFile is the index inside RecordingsDir, one
// RecordingRecord per line.
const RecordingsIndexFile = "recordings.jsonl"

// HumanActionsFile holds one HumanAction per line for a run a person took
// control of.
const HumanActionsFile = "human_actions.jsonl"

// Recording caps. A recording over its cap is dropped and noted in its
// record; the run's recordings together never exceed MaxRunRecordingBytes.
const (
	DefaultRecordingBytes int64 = 128 << 20
	MaxRunRecordingBytes  int64 = 256 << 20
	// DefaultRecordingWaitS is how long the workspace waits for a service
	// to stop after its stop command before it copies the files anyway.
	DefaultRecordingWaitS = 20
	MaxRecordingWaitS     = 120
)

// Recording kinds (RecordingFile.Kind).
const (
	RecordingVideo   = "video"
	RecordingHAR     = "har"
	RecordingConsole = "console"
	RecordingLog     = "log"
)

// RecordingKinds lists the valid kinds.
var RecordingKinds = []string{RecordingVideo, RecordingHAR, RecordingConsole, RecordingLog}

// RecordingsSpec is the recordings: block of a service.
type RecordingsSpec struct {
	// Stop runs inside the service container once the state exports are
	// read, so the service can finish its files (close the browser context,
	// stop ffmpeg). Empty means the files are complete as they are. When
	// Stop exits 0 the files are copied at once. A stop command may instead
	// stop its own container (its exec then fails or is cut off): the
	// workspace waits for the container to stop, and copies the files from
	// the stopped container.
	Stop []string `json:"stop,omitempty" yaml:"stop"`
	// WaitS bounds that wait; default DefaultRecordingWaitS. When the
	// container keeps running the files are copied at the deadline.
	WaitS int `json:"wait_s,omitempty" yaml:"wait_s"`
	// Files are what is copied out, each a file or a folder.
	Files []RecordingFile `json:"files" yaml:"files"`
}

// RecordingFile is one recording of a service: a file, or a folder whose
// regular files with a known extension are copied (a Playwright video
// folder holds one .webm per page).
type RecordingFile struct {
	ID       string `json:"id" yaml:"id"`
	Path     string `json:"path" yaml:"path"`
	Kind     string `json:"kind" yaml:"kind"`                     // video | har | console | log
	MaxBytes int64  `json:"max_bytes,omitempty" yaml:"max_bytes"` // default DefaultRecordingBytes
}

// LiveSpec is the live: block of a service: how the engine reads its screen
// while the agent runs and, in a debug run, sends a person's input to it.
type LiveSpec struct {
	// Frame prints one PNG or JPEG image of the screen to stdout.
	Frame []string `json:"frame" yaml:"frame"`
	// Input reads one HumanAction JSON object on stdin and performs it.
	// Empty: the service cannot be taken over.
	Input []string `json:"input,omitempty" yaml:"input"`
}

// LiveFrameEvery is how often the engine samples a running service's screen.
const LiveFrameEvery = 2 * time.Second

// MaxLiveFrameBytes caps one live frame, so a worker's post of it (base64
// in JSON) stays under the API's 1 MiB body limit. Services send small
// JPEG thumbnails.
const MaxLiveFrameBytes = 640 << 10

// RecordingRecord is one recorded file in the run folder.
type RecordingRecord struct {
	Service string `json:"service"`
	ID      string `json:"id"`             // the RecordingFile id
	Kind    string `json:"kind"`           // video | har | console | log
	File    string `json:"file,omitempty"` // name inside RecordingsDir/<service>/
	Mime    string `json:"mime,omitempty"`
	Bytes   int64  `json:"bytes,omitempty"`
	SHA256  string `json:"sha256,omitempty"`
	// StartedAt is when the recording began (a video's first frame), read
	// from a "<file>.start" sidecar the service writes (epoch milliseconds or
	// RFC 3339). Zero when the service wrote none.
	StartedAt time.Time `json:"started_at,omitzero"`
	// Error says why the recording is missing: over its cap, not written,
	// the copy failed. File is empty then.
	Error string `json:"error,omitempty"`
}

// Human takeover actions (HumanAction.Action).
const (
	HumanClick  = "click"
	HumanType   = "type"
	HumanKey    = "key"
	HumanScroll = "scroll"
)

// HumanAction is one input a person sent to a run's screen in a debug run.
type HumanAction struct {
	Seq     int       `json:"seq"`
	At      time.Time `json:"at"`
	Service string    `json:"service"`
	Action  string    `json:"action"` // click | type | key | scroll
	X       int       `json:"x,omitempty"`
	Y       int       `json:"y,omitempty"`
	Text    string    `json:"text,omitempty"`
	Combo   string    `json:"combo,omitempty"`
	// Direction of a scroll: up | down | left | right.
	Direction string `json:"direction,omitempty"`
	// FrameW and FrameH are the size of the live frame X and Y were read
	// from; the service scales them to its screen (a desktop's frame is a
	// smaller thumbnail). Zero means X and Y are screen pixels already.
	FrameW int    `json:"frame_w,omitempty"`
	FrameH int    `json:"frame_h,omitempty"`
	By     string `json:"by,omitempty"`      // who sent it (user id or email), as the server saw it
	ByName string `json:"by_name,omitempty"` // how By reads in GET /runs/{id}/recordings (a member's display name); never stored
	Error  string `json:"error,omitempty"`   // the service refused or failed the input
}

// MaxHumanText caps the text of one type action.
const MaxHumanText = 2000

// LiveFrame is the newest screen image of a running run.
type LiveFrame struct {
	RunID   string    `json:"run_id"`
	Service string    `json:"service"`
	Mime    string    `json:"mime"`
	At      time.Time `json:"at"`
	Data    []byte    `json:"data"` // base64 in JSON
	// Debug says the run is a debug run, the only kind a person may take
	// control of; TakeoverOK adds that the service has an input command.
	Debug      bool `json:"debug,omitempty"`
	TakeoverOK bool `json:"takeover_ok,omitempty"`
}

// LiveFrameReply answers a worker's live frame post: the human inputs
// queued for the run since the last post, oldest first.
type LiveFrameReply struct {
	Inputs []HumanAction `json:"inputs,omitempty"`
}

// LiveService is one service of a run whose screen can be shown live.
type LiveService struct {
	ID       string `json:"id"`
	Takeover bool   `json:"takeover,omitempty"` // it has an input command
}

// LiveScreen is implemented by an environment whose services can show their
// screen while the agent runs (the Docker workspace, environments/recordings.go).
type LiveScreen interface {
	// LiveServices lists the run's services with a live block.
	LiveServices(rc *RunContext) []LiveService
	// LiveFrame returns one screen image of the service and its media type.
	LiveFrame(ctx context.Context, rc *RunContext, service string) ([]byte, string, error)
	// LiveInput performs one person's input on the service.
	LiveInput(ctx context.Context, rc *RunContext, service string, a HumanAction) error
}
