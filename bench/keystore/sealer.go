package keystore

import "fmt"

// Sealer encrypts other server-held secrets of one org with the keystore's
// master key and scheme (AES-256-GCM, org and id as associated data), such
// as webhook signing secrets (lane X16). Purpose namespaces the ids, so a
// value sealed for one use never opens as another, nor as a stored key.
type Sealer struct {
	master  []byte
	orgID   string
	purpose string
}

// NewSealer returns the sealer of orgID for purpose.
func NewSealer(master []byte, orgID, purpose string) (*Sealer, error) {
	if len(master) != 32 {
		return nil, ErrNoMasterKey
	}
	if orgID == "" || purpose == "" {
		return nil, fmt.Errorf("%w: sealer needs an org and a purpose", ErrInvalid)
	}
	return &Sealer{master: master, orgID: orgID, purpose: purpose}, nil
}

// Seal encrypts plaintext for id.
func (s *Sealer) Seal(id, plaintext string) (string, error) {
	return seal(s.master, s.orgID, s.purpose+":"+id, plaintext)
}

// Open decrypts what Seal returned for the same id.
func (s *Sealer) Open(id, sealed string) (string, error) {
	return open(s.master, s.orgID, s.purpose+":"+id, sealed)
}
