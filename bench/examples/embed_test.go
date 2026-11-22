package examples

import (
	"bytes"
	"testing"
)

func TestAcmeNodeREADMEEmbedded(t *testing.T) {
	if !bytes.HasPrefix(AcmeNodeREADME, []byte("# ")) || !bytes.Contains(AcmeNodeREADME, []byte("acme-orders")) {
		t.Fatalf("acme-node README not embedded: %d bytes", len(AcmeNodeREADME))
	}
}
