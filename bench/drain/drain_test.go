package drain

import (
	"os"
	"path/filepath"
	"testing"
)

func TestStartStopAndSources(t *testing.T) {
	t.Cleanup(func() { Stop() })
	n := 2
	remove := AddSource(func() int { return n })
	defer remove()
	if st := Get(); st.Draining || st.Drained || st.InFlight != 2 {
		t.Fatalf("initial %+v", st)
	}
	st := Start("ops@example.com")
	if !st.Draining || st.Drained || st.By != "ops@example.com" || st.Since == nil || !Active() {
		t.Fatalf("started %+v", st)
	}
	first := *st.Since
	if again := Start("someone else"); again.By != "ops@example.com" || !again.Since.Equal(first) {
		t.Fatalf("a second start replaced the first: %+v", again)
	}
	n = 0
	if st := Get(); !st.Drained || st.InFlight != 0 {
		t.Fatalf("no lease in flight but not drained: %+v", st)
	}
	if st := Stop(); st.Draining || st.Drained || st.Since != nil || Active() {
		t.Fatalf("stopped %+v", st)
	}
	remove()
	if InFlight() != 0 {
		t.Fatal("removed source still counted")
	}
}

func TestTokenFile(t *testing.T) {
	t.Cleanup(func() { SetToken("") })
	p := TokenPath(filepath.Join(t.TempDir(), "runs"))
	if err := WriteToken(p); err != nil {
		t.Fatal(err)
	}
	fi, err := os.Stat(p)
	if err != nil || fi.Mode().Perm() != 0o600 {
		t.Fatalf("token file %v %v", fi, err)
	}
	tok, err := ReadToken(p)
	if err != nil || len(tok) != 48 {
		t.Fatalf("token %q %v", tok, err)
	}
	if !CheckToken(tok) || CheckToken(tok+"x") || CheckToken("") {
		t.Fatal("token check")
	}
	if err := WriteToken(p); err != nil {
		t.Fatal(err)
	}
	if CheckToken(tok) {
		t.Fatal("an old token still matches after a new one was written")
	}
}
