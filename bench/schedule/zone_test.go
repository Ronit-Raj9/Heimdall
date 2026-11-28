package schedule

import (
	"strings"
	"testing"
)

func TestZoneNames(t *testing.T) {
	for _, name := range []string{"Asia/Calcutta", "Asia/Kolkata", "UTC", "Europe/Kiev"} {
		s := Schedule{Name: "n", Cron: "0 6 * * *", Timezone: name}
		if _, err := s.Location(); err != nil {
			t.Errorf("%s: %v", name, err)
		}
	}
	if got := CanonicalZone("Asia/Calcutta"); got != "Asia/Kolkata" {
		t.Errorf("CanonicalZone(Asia/Calcutta) = %s", got)
	}
	_, err := Schedule{Name: "n", Timezone: "Mars/Olympus"}.Location()
	if err == nil || !strings.Contains(err.Error(), "pick another zone") {
		t.Errorf("wrong zone: %v", err)
	}
}

func TestFailureStreaks(t *testing.T) {
	dir := t.TempDir()
	p := dir + "/" + RecordsFile
	for _, r := range []Record{
		{Schedule: "a", Status: StatusError}, {Schedule: "a", Status: StatusOK},
		{Schedule: "a", Status: StatusError}, {Schedule: "a", Status: StatusSkipped},
		{Schedule: "a", Status: StatusRefused}, {Schedule: "a", Status: StatusError, Reason: "not ready"},
		{Schedule: "b", Status: StatusError}, {Schedule: "b", Status: StatusFailed},
	} {
		if err := appendRecord(p, r); err != nil {
			t.Fatal(err)
		}
	}
	n, why, err := FailureStreaks(dir)
	if err != nil || n["a"] != 3 || why["a"] != "not ready" || n["b"] != 0 {
		t.Fatalf("streaks %v %v %v", n, why, err)
	}
}
