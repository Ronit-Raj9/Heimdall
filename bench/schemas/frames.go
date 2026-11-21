package schemas

import "time"

// Screen replay of computer-use and browser runs. The tool proxy saves every
// screenshot the agent got as FramesDir/NNNN.png (or .jpg) next to its
// ledger and appends one FrameRecord line to FramesDir/FramesIndexFile; the
// workspace copies the folder into the run folder at the end of the run.

// FramesDir is the frames folder, next to the ledger and in the run folder.
const FramesDir = "frames"

// FramesIndexFile is the frame index inside FramesDir, one FrameRecord per line.
const FramesIndexFile = "frames.jsonl"

// FrameRecord is one saved screenshot.
type FrameRecord struct {
	File   string    `json:"file"`             // image file name inside FramesDir
	Seq    int       `json:"seq,omitempty"`    // ledger row of the call that returned it
	Time   time.Time `json:"ts"`               // when the call started
	Tool   string    `json:"tool,omitempty"`   // the tool that returned the image
	Action string    `json:"action,omitempty"` // the last acting call before it, e.g. "click {\"x\":10,\"y\":20}"
	Mime   string    `json:"mime,omitempty"`
}
