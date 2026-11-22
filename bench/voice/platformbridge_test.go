package voice

import (
	"os/exec"
	"testing"
)

// TestPlatformBridgePython runs platformbridge_test.py: the bridge to hosted
// voice agents (Twilio Media Streams, Vapi and ElevenLabs framings) between
// the real caller and fake platforms, barge-in included.
func TestPlatformBridgePython(t *testing.T) {
	if testing.Short() {
		t.Skip("short")
	}
	py, err := exec.LookPath("python3")
	if err != nil {
		t.Skip("python3 not installed")
	}
	cmd := exec.Command(py, "-B", "-m", "unittest", "platformbridge_test")
	cmd.Dir = "."
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("platformbridge_test.py: %v\n%s", err, out)
	}
}

func TestBridgeFilesEmbedded(t *testing.T) {
	for name, b := range Files(BridgeFiles) {
		if len(b) == 0 {
			t.Errorf("%s is empty", name)
		}
	}
}
