package gwfeature

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"time"
)

// SourceConfig is the org's saved connection to its own Bifrost, read by
// features that look at the org's prompt and skill repositories or its
// registered agents. CredentialSecret is an org secret name, never a value.
type SourceConfig struct {
	GatewayURL       string    `json:"gateway_url"`
	CredentialSecret string    `json:"credential_secret,omitempty"`
	SavedBy          string    `json:"saved_by,omitempty"`
	SavedAt          time.Time `json:"saved_at"`
}

func sourcePath(root string) string {
	return filepath.Join(root, ".heimdall", "gateway", "source.json")
}

// LoadSource reads the saved source; nil, nil when none is saved.
func LoadSource(root string) (*SourceConfig, error) {
	data, err := os.ReadFile(sourcePath(root))
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var c SourceConfig
	if err := json.Unmarshal(data, &c); err != nil {
		return nil, err
	}
	return &c, nil
}

// SaveSource saves the source, replacing any earlier one.
func SaveSource(root string, c SourceConfig) error {
	data, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}
	return writeFileAtomic(sourcePath(root), data)
}

// RemoveSource forgets the saved source; removing none is not an error.
func RemoveSource(root string) error {
	err := os.Remove(sourcePath(root))
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	return err
}
