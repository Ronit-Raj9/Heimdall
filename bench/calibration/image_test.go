package calibration

import (
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/maximhq/bifrost/bench/schemas"
)

// X1 vision grading: image items.

func TestItemIDsWithoutImageAreUnchanged(t *testing.T) {
	// Recorded before the image field existed: sha256("t1\x00Max")[:16] and
	// sha256("q1\x00Rex")[:16]. Existing labels.jsonl rows must keep them.
	if got := ItemID("t1", "Max"); got != "17390507ac69e3a5" {
		t.Fatalf("ItemID(t1, Max) = %s, want the pre-change 17390507ac69e3a5", got)
	}
	for _, c := range []struct{ task, answer, want string }{{"t1", "Max", "17390507ac69e3a5"}, {"q1", "Rex", "3276ec422fe94fa8"}} {
		if got := ItemIDOf(schemas.CalibrationItem{Task: c.task, Answer: c.answer}); got != c.want {
			t.Fatalf("ItemIDOf(%s, %s) = %s, want %s", c.task, c.answer, got, c.want)
		}
	}
	img := strings.Repeat("a", 64)
	if ItemIDWithImage("t1", "Max", img) == ItemID("t1", "Max") || ItemIDWithImage("t1", "", img) == ItemIDWithImage("t1", "", strings.Repeat("b", 64)) {
		t.Fatal("an image must change the id")
	}
}

func TestImageItemsValidateTheFile(t *testing.T) {
	root := t.TempDir()
	png := []byte("\x89PNG\r\n\x1a\n not really decoded here")
	sum, err := AddImage(root, png)
	if err != nil {
		t.Fatal(err)
	}
	if again, err := AddImage(root, png); err != nil || again != sum {
		t.Fatalf("AddImage twice: %s %v", again, err)
	}
	it := schemas.CalibrationItem{Task: "button", Image: sum, Label: schemas.CalibrationCorrect, Labeller: "ana", Source: schemas.CalibrationSourceLabel, At: t0}
	it.ID = ItemIDOf(it)
	if err := ValidateItem(it); err != nil {
		t.Fatalf("image item with no answer text: %v", err)
	}
	if n, err := Append(root, []schemas.CalibrationItem{it}); err != nil || n != 1 {
		t.Fatalf("Append = %d, %v", n, err)
	}
	s, err := LoadSet(root)
	if err != nil || len(s.Items) != 1 || s.Items[0].Image != sum {
		t.Fatalf("LoadSet: %+v %v", s, err)
	}
	bad := it
	bad.ID = ItemID(it.Task, it.Answer)
	if err := ValidateItem(bad); !errors.Is(err, ErrInput) {
		t.Fatalf("id without the image accepted: %v", err)
	}
	bad = it
	bad.Image = "ABC"
	if err := ValidateItem(bad); !errors.Is(err, ErrInput) {
		t.Fatalf("bad image sum accepted: %v", err)
	}
	if err := os.WriteFile(ImagePath(root, sum), []byte("changed"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadSet(root); !errors.Is(err, ErrInput) || !strings.Contains(err.Error(), "has sha256") {
		t.Fatalf("tampered image: %v", err)
	}
	missing := it
	missing.Image = strings.Repeat("c", 64)
	missing.ID = ItemIDOf(missing)
	if _, err := Append(root, []schemas.CalibrationItem{missing}); !errors.Is(err, ErrInput) || !strings.Contains(err.Error(), "is not in") {
		t.Fatalf("missing image: %v", err)
	}
	if _, err := AddImage(root, make([]byte, MaxImageBytes+1)); !errors.Is(err, ErrInput) {
		t.Fatalf("oversized image: %v", err)
	}
}
