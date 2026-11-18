package evidencestore

import (
	"bytes"
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/maximhq/bifrost/bench/utils"
)

func TestListingValidate(t *testing.T) {
	sum := strings.Repeat("a", 64)
	good := func() *Listing {
		return &Listing{Format: ListingFormat, OrgID: "local", ExpID: "e_1", Files: []FileEntry{
			{Path: "manifest.json", Size: 1, SHA256: sum}, {Path: "slots/s_1/slot.json", Size: 1, SHA256: sum}}}
	}
	cases := []struct {
		name   string
		mutate func(*Listing)
		want   string
	}{
		{"good", func(*Listing) {}, ""},
		{"format", func(l *Listing) { l.Format = "other/1" }, "format"},
		{"other experiment", func(l *Listing) { l.ExpID = "e_2" }, "names local/e_2"},
		{"escaping path", func(l *Listing) { l.Files[1].Path = "slots/../../x" }, "invalid"},
		{"listing name", func(l *Listing) { l.Files[0].Path = ".mirror.json"; l.Files[0], l.Files[1] = l.Files[1], l.Files[0] }, "reserved"},
		{"temp segment", func(l *Listing) { l.Files[1].Path = "slots/" + tmpPrefix + "1" }, "reserved"},
		{"dot file in a run", func(l *Listing) { l.Files[1].Path = "slots/s_1/runs/r_1/.hidden" }, ""},
		{"dot file outside a run", func(l *Listing) { l.Files[1].Path = "slots/s_1/.hidden" }, "dot name"},
		{"lease", func(l *Listing) { l.Files[1].Path = "slots/s_1/lease.json" }, "operational"},
		{"stub", func(l *Listing) { l.Files[1].Path = "archived.json"; l.Files[0], l.Files[1] = l.Files[1], l.Files[0] }, "stub"},
		{"file under runs", func(l *Listing) { l.Files[1].Path = "slots/s_1/runs/junk" }, "only run folders"},
		{"unsorted", func(l *Listing) { l.Files[0], l.Files[1] = l.Files[1], l.Files[0] }, "sorted"},
		{"repeated", func(l *Listing) { l.Files[1] = l.Files[0] }, "sorted"},
		{"bad sha", func(l *Listing) { l.Files[0].SHA256 = "xyz" }, "sha256"},
		{"negative size", func(l *Listing) { l.Files[0].Size = -1 }, "size"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			l := good()
			c.mutate(l)
			err := l.validate("local", "e_1")
			if c.want == "" && err != nil || c.want != "" && (err == nil || !strings.Contains(err.Error(), c.want)) {
				t.Fatalf("validate: %v, want %q", err, c.want)
			}
		})
	}
}

func TestReadListingRejectsUnknownFields(t *testing.T) {
	ctx := context.Background()
	_, d := newDirMirror(t)
	b := []byte(`{"format":"` + ListingFormat + `","org_id":"local","exp_id":"e_1","files":[],"surprise":1}`)
	if err := d.Put(ctx, listingKey("local", "e_1"), PutInput{Body: bytes.NewReader(b), Size: int64(len(b)), SHA256: utils.SHA256Bytes(b)}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := readListing(ctx, d, "local", "e_1"); err == nil || !strings.Contains(err.Error(), "surprise") {
		t.Fatalf("unknown field: %v", err)
	}
}

func TestMirroredExperiments(t *testing.T) {
	ctx := context.Background()
	_, d := newDirMirror(t)
	for _, k := range []string{"local/e_b/.mirror.json", "local/e_a/.mirror.json", "local/e_a/manifest.json",
		"local/e_c/manifest.json", "org_x/e_z/.mirror.json", "local/e_d/slots/.mirror.json"} {
		b := []byte("{}")
		if err := d.Put(ctx, k, PutInput{Body: bytes.NewReader(b), Size: 2, SHA256: utils.SHA256Bytes(b)}); err != nil {
			t.Fatal(err)
		}
	}
	got, err := MirroredExperiments(ctx, d, "local")
	if err != nil || strings.Join(got, ",") != "e_a,e_b" {
		t.Fatalf("MirroredExperiments: %v %v", got, err)
	}
	if _, err := MirroredExperiments(ctx, d, "../x"); err == nil {
		t.Fatal("bad org id accepted")
	}
}

func TestMirroredOrgsAndOpenOrg(t *testing.T) {
	ctx := context.Background()
	_, d := newDirMirror(t)
	for _, k := range []string{"local/e_a/.mirror.json", "org_x/e_b/.mirror.json", "org_y/e_c/manifest.json", "_backups/db/x.json"} {
		b := []byte("{}")
		if err := d.Put(ctx, k, PutInput{Body: bytes.NewReader(b), Size: 2, SHA256: utils.SHA256Bytes(b)}); err != nil {
			t.Fatal(err)
		}
	}
	got, err := MirroredOrgs(ctx, d)
	if err != nil || strings.Join(got, ",") != "local,org_x" {
		t.Fatalf("MirroredOrgs: %v %v", got, err)
	}
	runs := t.TempDir()
	o, err := OpenOrg(runs, "org_x", nil)
	if err != nil || o.Store.Root() != filepath.Join(runs, "orgs", "org_x") {
		t.Fatalf("OpenOrg: %+v %v", o, err)
	}
	if o, err := OpenOrg(runs, LocalOrgID, nil); err != nil || o.Store.Root() != runs {
		t.Fatalf("OpenOrg local: %v", err)
	}
	if _, err := OpenOrg(runs, "../x", nil); err == nil {
		t.Fatal("bad org id accepted")
	}
}
