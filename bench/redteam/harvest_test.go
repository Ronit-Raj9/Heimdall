package redteam

import "testing"

func TestHarvestedMarker(t *testing.T) {
	dir := t.TempDir()
	if Harvested(dir, "ev_1") {
		t.Fatal("fresh folder reports harvested")
	}
	if err := MarkHarvested(dir, "ev_1"); err != nil {
		t.Fatal(err)
	}
	if err := MarkHarvested(dir, "ev_1"); err != nil || !Harvested(dir, "ev_1") || Harvested(dir, "ev_2") {
		t.Fatal("marker")
	}
}
