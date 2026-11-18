package browsecompplus

import "testing"

// Vectors: the first three were computed on 2026-11-18 with the authors'
// own derive_key and XOR code (copied from the dataset card) on invented
// strings; the last two are real fields of Tevatron/browsecomp-plus
// (data/test-00003-of-00006.parquet, revision 144cff8e, query 655, first
// hard negative: its docid and its url), which are not answers.
var vectors = []struct{ plain, cipher string }{
	{"Marrow Gull", "bUkOgeG9OO5gV9E="},
	{"A test vector longer than thirty-two bytes, so the key repeats: done.", "YQgIlv2+ON9wWMn6qJF6urlV2Nt/7Z8ha7c4upfB1ksNXAucrqhh3XBIkbWp3jahv1edwjrg1zJg5ymzisCYEkRHEpag"},
	{"Zoë \u2013 ünïcode", "eke/WK4omDo1+AH7GR51urNX"},
	{"18480", "ERBIy74="},
	{"https://bearmccreary.com/god-of-war-ragnarok-valhalla/", "SFwIg/3wN4Z3Xtznt9J1p7JTz9Bx+pgtKvAjttPcxB9XSQ7e/Kt/x3RJ0v73x3e5v1PRxT62"},
}

func TestDecryptVectors(t *testing.T) {
	for _, v := range vectors {
		got, err := Decrypt(v.cipher, Canary)
		if err != nil || got != v.plain {
			t.Errorf("Decrypt(%q) = %q, %v; want %q", v.cipher, got, err, v.plain)
		}
		if enc := Encrypt(v.plain, Canary); enc != v.cipher {
			t.Errorf("Encrypt(%q) = %q, want %q", v.plain, enc, v.cipher)
		}
	}
}

func TestDecryptRefusesPlainText(t *testing.T) {
	for _, s := range []string{"Who wrote the novel?", "not base64!", ""} {
		if got, err := Decrypt(s, Canary); err == nil && s != "" {
			t.Errorf("Decrypt(%q) = %q, want an error", s, got)
		}
	}
	if _, err := Decrypt("bUkOgeG9OO5gV9E=", "another password"); err == nil {
		// XOR with a wrong key may still be UTF-8 by chance; this vector is not.
		t.Error("a wrong password decrypted the vector to UTF-8")
	}
}
