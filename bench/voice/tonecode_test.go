package voice

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestToneRoundTrip(t *testing.T) {
	for _, s := range []string{"", "a", "I have refunded the full 45.00 for order B-3001.", "naïve café ✓"} {
		pcm := ToneEncode(s, 137, 300)
		got, ok := ToneDecode(pcm)
		if s == "" {
			if !ok || got != "" {
				t.Fatalf("empty text: %q %v", got, ok)
			}
			continue
		}
		if !ok || got != s {
			t.Fatalf("round trip %q: got %q ok %v", s, got, ok)
		}
		// Resampled to 24 kHz and back, as the Realtime path does.
		if got, ok := ToneDecode(Resample(Resample(pcm, 16000, 24000), 24000, 16000)); !ok || got != s {
			t.Fatalf("resampled %q: got %q", s, got)
		}
	}
	if _, ok := ToneDecode(make([]byte, 32000)); ok {
		t.Fatal("silence decoded as a code")
	}
	two := append(ToneEncode("one", 0, 100), ToneEncode("two", 50, 0)...)
	if got, _ := ToneDecode(two); got != "one two" {
		t.Fatalf("two bursts: %q", got)
	}
}

func TestWAV(t *testing.T) {
	pcm := ToneEncode("hello", 0, 0)
	got, rate, err := ReadWAV(WAV(pcm, 24000))
	if err != nil || rate != 24000 || !bytes.Equal(got, pcm) {
		t.Fatalf("wav: rate %d err %v equal %v", rate, err, bytes.Equal(got, pcm))
	}
	if _, _, err := ReadWAV([]byte("RIFFxxxxWAVEnope")); err == nil {
		t.Fatal("broken wav accepted")
	}
}

// TestToneMatchesPython checks the Go and Python encoders give the same
// bytes and decode each other's audio.
func TestToneMatchesPython(t *testing.T) {
	py, err := exec.LookPath("python3")
	if err != nil {
		t.Skip("python3 not installed")
	}
	dir := t.TempDir()
	out := filepath.Join(dir, "py.pcm")
	cmd := exec.Command(py, "-B", "-c", "import sys, tonecode; open(sys.argv[1], 'wb').write(tonecode.encode('Order B-3005: 12.00 refunded', 40, 60))", out)
	cmd.Dir = "."
	if b, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("python: %v\n%s", err, b)
	}
	pyPCM, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	if goPCM := ToneEncode("Order B-3005: 12.00 refunded", 40, 60); !bytes.Equal(goPCM, pyPCM) {
		t.Fatalf("encoders differ: go %d bytes, python %d bytes", len(goPCM), len(pyPCM))
	}
	if got, ok := ToneDecode(pyPCM); !ok || got != "Order B-3005: 12.00 refunded" {
		t.Fatalf("go decode of python audio: %q", got)
	}
}
