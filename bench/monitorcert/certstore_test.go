package monitorcert

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/maximhq/bifrost/bench/schemas"
)

func testCert(id string, at time.Time) *schemas.MonitorCertificate {
	rate := 0.5
	return &schemas.MonitorCertificate{CertID: id, CreatedAt: at, ExpIDs: []string{"e_x"},
		Monitor:  schemas.MonitorCertMonitor{Name: "kw", Kind: "builtin", Mode: "transcript"},
		Labels:   schemas.MonitorLabelSummary{Honest: 3, Attack: 2},
		Headline: &schemas.MonitorCertHeadline{TargetFPR: 0.01, CatchRate: &rate, CatchInterval: []float64{0.1, 0.9}}}
}

func TestCertStore(t *testing.T) {
	root := t.TempDir()
	s, err := NewCertStore(filepath.Join(root, "certs"), filepath.Join(root, "runs"))
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	empty, err := s.List(ctx)
	if err != nil || len(empty.Certifications) != 0 || empty.Certifications == nil {
		t.Fatalf("empty list %+v err %v", empty, err)
	}
	t0 := time.Date(2026, 11, 18, 1, 0, 0, 0, time.UTC)
	for i, id := range []string{"mc_old", "mc_new"} {
		if _, err := s.Write(testCert(id, t0.Add(time.Duration(i)*time.Hour)), []byte("# "+id+"\n")); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := s.Write(testCert("mc_old", t0), nil); !errors.Is(err, schemas.ErrExists) {
		t.Errorf("duplicate write: %v", err)
	}
	if _, err := s.Write(testCert("../x", t0), nil); err == nil {
		t.Error("bad id written")
	}
	if err := os.MkdirAll(filepath.Join(root, "certs", "mc_broken"), 0o755); err != nil {
		t.Fatal(err)
	}
	list, err := s.List(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(list.Certifications) != 2 || list.Certifications[0].CertID != "mc_new" || list.Certifications[1].Honest != 3 ||
		list.Certifications[0].Headline == nil || list.Certifications[0].Warnings == nil {
		t.Errorf("list %+v", list.Certifications)
	}
	tests := []struct {
		name    string
		id      string
		wantErr error
	}{
		{"found", "mc_old", nil},
		{"unknown", "mc_none", schemas.ErrNotFound},
		{"traversal", "../certs/mc_old", schemas.ErrNotFound},
		{"no certificate file", "mc_broken", schemas.ErrNotFound},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			c, err := s.Get(ctx, tc.id)
			if !errors.Is(err, tc.wantErr) {
				t.Fatalf("get err %v, want %v", err, tc.wantErr)
			}
			md, rerr := s.Report(ctx, tc.id)
			if !errors.Is(rerr, tc.wantErr) {
				t.Fatalf("report err %v", rerr)
			}
			if tc.wantErr == nil && (c.CertID != tc.id || string(md) != "# mc_old\n") {
				t.Errorf("cert %+v report %q", c, md)
			}
		})
	}
}

func TestNewCertStoreRefusesRunsRoot(t *testing.T) {
	root := t.TempDir()
	tests := []struct {
		name    string
		certs   string
		wantErr bool
	}{
		{"inside runs root", filepath.Join(root, "runs", "monitor-certs"), true},
		{"the runs root", filepath.Join(root, "runs"), true},
		{"beside it", filepath.Join(root, "monitor-certs"), false},
		{"name prefix only", filepath.Join(root, "runs-certs"), false},
		{"empty", "", true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, err := NewCertStore(tc.certs, filepath.Join(root, "runs"))
			if (err != nil) != tc.wantErr {
				t.Errorf("err %v", err)
			}
		})
	}
}

func TestDefaultRoot(t *testing.T) {
	t.Setenv(RootEnv, "")
	if got := DefaultRoot("/bench"); got != filepath.Join("/bench", "monitor-certs") {
		t.Errorf("default %s", got)
	}
	t.Setenv(RootEnv, "/elsewhere")
	if got := DefaultRoot("/bench"); got != "/elsewhere" {
		t.Errorf("env %s", got)
	}
}
