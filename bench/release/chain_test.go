package release

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/maximhq/bifrost/bench/utils"
)

// chained creates n releases and returns the fixture and the log path.
func chained(t *testing.T, n int) (*fixture, string) {
	t.Helper()
	f := newFixture(t, schemas.ActionShip, schemas.PolicyAllow)
	for i := 0; i < n; i++ {
		if _, err := f.log.Create(context.Background(), request("ana", "")); err != nil {
			t.Fatalf("Create %d: %v", i, err)
		}
	}
	return f, filepath.Join(f.log.Root(), LogFile)
}

func TestChainAppendAndVerify(t *testing.T) {
	f, _ := chained(t, 3)
	list, err := f.log.List(context.Background())
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if !list.ChainOK || len(list.Releases) != 3 {
		t.Fatalf("chain_ok %v, %d releases", list.ChainOK, len(list.Releases))
	}
	prev := ""
	for i, rec := range list.Releases {
		if rec.PrevHash != prev {
			t.Errorf("record %d prev_hash %q, want %q", i, rec.PrevHash, prev)
		}
		prev = rec.Hash
	}
	if err := f.log.VerifyChain(); err != nil {
		t.Errorf("VerifyChain: %v", err)
	}
	if n, err := f.log.Verify(context.Background()); err != nil || n != 3 {
		t.Errorf("Verify = %d, %v", n, err)
	}
}

func TestEmptyLog(t *testing.T) {
	f := newFixture(t, schemas.ActionShip, "")
	list, err := f.log.List(context.Background())
	if err != nil || !list.ChainOK || list.Releases == nil || len(list.Releases) != 0 {
		t.Fatalf("List = %+v, %v", list, err)
	}
	if _, err := f.log.Get(context.Background(), "rel_none"); !errors.Is(err, schemas.ErrNotFound) {
		t.Errorf("Get = %v", err)
	}
}

func TestChainDetectsTampering(t *testing.T) {
	tests := []struct {
		name   string
		edit   func(t *testing.T, lines [][]byte) [][]byte
		reason string
	}{
		{"edit a field", func(t *testing.T, lines [][]byte) [][]byte {
			lines[1] = bytes.Replace(lines[1], []byte(`"approver":"ana"`), []byte(`"approver":"bob"`), 1)
			return lines
		}, "edited"},
		{"edit a field and rehash it", func(t *testing.T, lines [][]byte) [][]byte {
			lines[1] = rehashed(t, lines[1], func(r *schemas.ReleaseRecord) { r.Approver = "bob" })
			return lines
		}, "prev_hash"},
		{"reformat a line", func(t *testing.T, lines [][]byte) [][]byte {
			lines[0] = bytes.Replace(lines[0], []byte(`,"exp_id"`), []byte(`, "exp_id"`), 1)
			return lines
		}, "canonical"},
		{"add a field", func(t *testing.T, lines [][]byte) [][]byte {
			lines[0] = bytes.Replace(lines[0], []byte(`{`), []byte(`{"signed":true,`), 1)
			return lines
		}, "unknown field"},
		{"delete a line", func(t *testing.T, lines [][]byte) [][]byte {
			return append(lines[:1], lines[2:]...)
		}, "prev_hash"},
		{"swap lines", func(t *testing.T, lines [][]byte) [][]byte {
			lines[0], lines[1] = lines[1], lines[0]
			return lines
		}, "prev_hash"},
		{"torn last line", func(t *testing.T, lines [][]byte) [][]byte {
			lines[2] = lines[2][:len(lines[2])/2]
			return lines
		}, "line 3"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f, path := chained(t, 3)
			b, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			lines := bytes.Split(bytes.TrimSuffix(b, []byte{'\n'}), []byte{'\n'})
			out := bytes.Join(tt.edit(t, lines), []byte{'\n'})
			if tt.name != "torn last line" {
				out = append(out, '\n')
			}
			if err := os.WriteFile(path, out, 0o644); err != nil {
				t.Fatal(err)
			}
			err = f.log.VerifyChain()
			if !errors.Is(err, ErrChainBroken) || !strings.Contains(err.Error(), tt.reason) {
				t.Fatalf("VerifyChain = %v, want %q", err, tt.reason)
			}
			list, err := f.log.List(context.Background())
			if err != nil || list.ChainOK {
				t.Errorf("List chain_ok = %v, %v", list.ChainOK, err)
			}
			_, err = f.log.Create(context.Background(), request("ana", ""))
			var refused *RefusedError
			if !errors.As(err, &refused) || !errors.Is(err, ErrChainBroken) {
				t.Errorf("Create on a broken chain = %v, want refusal", err)
			}
		})
	}
}

// rehashed decodes a line, applies edit, recomputes its own hash and
// re-encodes it: a forger who knows the format. Only the next link catches it.
func rehashed(t *testing.T, line []byte, edit func(*schemas.ReleaseRecord)) []byte {
	t.Helper()
	rec, err := decodeLine(line)
	if err != nil {
		t.Fatal(err)
	}
	edit(rec)
	if rec.Hash, err = recordHash(rec); err != nil {
		t.Fatal(err)
	}
	out, err := utils.CanonicalJSON(rec)
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func TestVerifyDetectsSwappedEvidence(t *testing.T) {
	f, _ := chained(t, 2)
	list, err := f.log.List(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(f.log.Root(), list.Releases[1].EvidencePath)
	if err := os.Chmod(p, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte("not the pack"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := f.log.VerifyChain(); err != nil {
		t.Errorf("VerifyChain checks the log only: %v", err)
	}
	if _, err := f.log.Verify(context.Background()); !errors.Is(err, ErrEvidenceMismatch) {
		t.Errorf("Verify = %v, want ErrEvidenceMismatch", err)
	}
	if _, err := f.log.Export(context.Background(), list.Releases[1].ReleaseID); !errors.Is(err, ErrEvidenceMismatch) {
		t.Errorf("Export = %v, want ErrEvidenceMismatch", err)
	}
	if _, err := f.log.Evidence(context.Background(), list.Releases[0].ReleaseID); err != nil {
		t.Errorf("Evidence of the untouched release: %v", err)
	}
}
