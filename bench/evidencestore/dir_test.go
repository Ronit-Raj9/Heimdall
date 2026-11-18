package evidencestore

import (
	"bytes"
	"context"
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/maximhq/bifrost/bench/utils"
)

func TestDirBlob(t *testing.T) {
	ctx := context.Background()
	d, err := NewDir(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	put := func(key, body string) error {
		return d.Put(ctx, key, PutInput{Body: strings.NewReader(body), Size: int64(len(body)), SHA256: utils.SHA256Bytes([]byte(body))})
	}
	for _, k := range []string{"o/e/a.json", "o/e/slots/s/x.txt", "o/f/b", "o2/z"} {
		if err := put(k, "body of "+k); err != nil {
			t.Fatalf("put %s: %v", k, err)
		}
	}
	rc, info, err := d.Get(ctx, "o/e/a.json")
	if err != nil {
		t.Fatal(err)
	}
	b, _ := io.ReadAll(rc)
	rc.Close()
	if string(b) != "body of o/e/a.json" || info.Size != int64(len(b)) {
		t.Fatalf("get: %q %+v", b, info)
	}
	h, err := d.Head(ctx, "o/e/a.json")
	if err != nil || h.SHA256 != utils.SHA256Bytes(b) {
		t.Fatalf("head: %+v %v", h, err)
	}
	if _, err := d.Head(ctx, "o/e/none"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("head missing: %v", err)
	}
	list, err := d.List(ctx, "o/")
	if err != nil {
		t.Fatal(err)
	}
	var keys []string
	for _, o := range list {
		keys = append(keys, o.Key)
	}
	if strings.Join(keys, ",") != "o/e/a.json,o/e/slots/s/x.txt,o/f/b" {
		t.Fatalf("list o/: %v", keys)
	}
	if list, _ := d.List(ctx, "o/e/a"); len(list) != 1 {
		t.Fatalf("list by string prefix: %v", list)
	}
	if list, _ := d.List(ctx, "nothing/"); len(list) != 0 {
		t.Fatalf("list missing prefix: %v", list)
	}
	// A body that does not hash to the claim is refused and leaves the old
	// object.
	err = d.Put(ctx, "o/e/a.json", PutInput{Body: strings.NewReader("evil"), Size: 4, SHA256: utils.SHA256Bytes([]byte("good"))})
	if !errors.Is(err, ErrChecksum) {
		t.Fatalf("bad checksum: %v", err)
	}
	if h2, _ := d.Head(ctx, "o/e/a.json"); h2.SHA256 != h.SHA256 {
		t.Fatal("refused put replaced the object")
	}
	for _, k := range []string{"", "/abs", "a/../b", "a//b", "a/./b", `a\b`, "a/\x01", "x/" + tmpPrefix + "1", "trailing/"} {
		if err := put(k, "x"); err == nil {
			t.Errorf("key %q accepted", k)
		}
	}
	if err := d.Delete(ctx, "o/e/slots/s/x.txt"); err != nil {
		t.Fatal(err)
	}
	if err := d.Delete(ctx, "o/e/slots/s/x.txt"); err != nil {
		t.Fatalf("delete twice: %v", err)
	}
	if _, err := os.Stat(filepath.Join(d.Root(), "o", "e", "slots")); !errors.Is(err, fs.ErrNotExist) {
		t.Fatal("empty folders left after delete")
	}
	// Temporary files of an interrupted put are never listed.
	if err := os.WriteFile(filepath.Join(d.Root(), "o", "e", tmpPrefix+"123"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if list, _ := d.List(ctx, "o/e/"); len(list) != 1 {
		t.Fatalf("temp file listed: %v", list)
	}
}

func TestVerifyingReader(t *testing.T) {
	body := []byte("hello evidence")
	sum := utils.SHA256Bytes(body)
	if _, err := io.ReadAll(VerifyingReader(bytes.NewReader(body), int64(len(body)), sum)); err != nil {
		t.Fatal(err)
	}
	for name, r := range map[string]io.Reader{
		"changed": VerifyingReader(bytes.NewReader([]byte("hellO evidence")), int64(len(body)), sum),
		"short":   VerifyingReader(bytes.NewReader(body[:4]), int64(len(body)), sum),
		"long":    VerifyingReader(bytes.NewReader(append(body, '!')), int64(len(body)), sum),
	} {
		if _, err := io.ReadAll(r); !errors.Is(err, ErrChecksum) {
			t.Errorf("%s: %v", name, err)
		}
	}
}
