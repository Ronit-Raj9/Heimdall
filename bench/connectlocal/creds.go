package connectlocal

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Credentials is what `heimdall login` stores: one org API token per
// server, in a 0600 file under the user's config folder
// (~/.config/heimdall/credentials.json, or $HEIMDALL_CONFIG_DIR). The
// Python package's `heimdall serve` reads the same file.
type Credentials struct {
	// Default is the server used when none is given.
	Default string                 `json:"default,omitempty"`
	Servers map[string]*ServerCred `json:"servers"`
}

// ServerCred is the login for one server.
type ServerCred struct {
	Token     string    `json:"token"`
	OrgID     string    `json:"org_id,omitempty"`
	Org       string    `json:"org,omitempty"`
	Role      string    `json:"role,omitempty"`
	Scopes    []string  `json:"scopes,omitempty"`
	TokenID   string    `json:"token_id,omitempty"`
	CreatedAt time.Time `json:"created_at"`
	ExpiresAt time.Time `json:"expires_at,omitempty"`
	// Local is true for a local server's write token.
	Local bool `json:"local,omitempty"`
}

// ConfigDir is where the CLI keeps its files.
func ConfigDir() (string, error) {
	if d := os.Getenv("HEIMDALL_CONFIG_DIR"); d != "" {
		return d, nil
	}
	if d := os.Getenv("XDG_CONFIG_HOME"); d != "" {
		return filepath.Join(d, "heimdall"), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".config", "heimdall"), nil
}

// CredentialsPath is the credentials file.
func CredentialsPath() (string, error) {
	d, err := ConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(d, "credentials.json"), nil
}

// NormalizeServer is the key a server is stored under: scheme, host and
// path, no trailing slash.
func NormalizeServer(s string) string {
	return strings.TrimRight(strings.TrimSpace(s), "/")
}

// LoadCredentials reads the file; a missing file is empty credentials.
func LoadCredentials() (*Credentials, error) {
	p, err := CredentialsPath()
	if err != nil {
		return nil, err
	}
	c := &Credentials{Servers: map[string]*ServerCred{}}
	b, err := os.ReadFile(p)
	if errors.Is(err, os.ErrNotExist) {
		return c, nil
	}
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal(b, c); err != nil {
		return nil, err
	}
	if c.Servers == nil {
		c.Servers = map[string]*ServerCred{}
	}
	return c, nil
}

// Save writes the file with mode 0600 (the folder 0700).
func (c *Credentials) Save() (string, error) {
	p, err := CredentialsPath()
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		return "", err
	}
	b, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return "", err
	}
	tmp := p + ".tmp"
	if err := os.WriteFile(tmp, append(b, '\n'), 0o600); err != nil {
		return "", err
	}
	if err := os.Chmod(tmp, 0o600); err != nil {
		return "", err
	}
	return p, os.Rename(tmp, p)
}

// Resolve picks the server and token for a command: explicit values
// first, then HEIMDALL_URL and HEIMDALL_TOKEN, then the stored login (for
// that server, or the default one). The source says where the token came
// from (flag, env, login or "").
func Resolve(server, token string) (srv, tok, source string) {
	srv = NormalizeServer(server)
	if srv == "" {
		srv = NormalizeServer(os.Getenv("HEIMDALL_URL"))
	}
	if token != "" {
		return srv, token, "flag"
	}
	if t := strings.TrimSpace(os.Getenv("HEIMDALL_TOKEN")); t != "" {
		return srv, t, "env"
	}
	c, err := LoadCredentials()
	if err != nil {
		return srv, "", ""
	}
	if srv == "" {
		srv = c.Default
	}
	if sc := c.Servers[srv]; sc != nil && sc.Token != "" {
		if !sc.ExpiresAt.IsZero() && time.Now().After(sc.ExpiresAt) {
			return srv, "", ""
		}
		return srv, sc.Token, "login"
	}
	return srv, "", ""
}
