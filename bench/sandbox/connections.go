package sandbox

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/maximhq/bifrost/bench/keystore"
)

// Connections keeps each organization's platform connections in its
// keystore (Kind secret, AES-GCM at rest): one secret per connected
// platform whose value holds the token and endpoint, and one secret naming
// the org's default worker. The token is revealed only to build a client
// for a test or for the shared runner's assignment; views carry its last
// four characters only.
type Connections struct {
	Keys keystore.Store
	// Build builds a client; nil uses New.
	Build func(name string, c Credentials) (Platform, error)
	Now   func() time.Time
}

// Keystore markers.
const (
	providerPrefix  = "worker-platform:"
	providerDefault = "worker-platform-default"
	// Actor is the audit actor of keystore writes this package makes for
	// itself (clearing a default).
	Actor = "heimdall:worker-platforms"
)

// Errors.
var (
	ErrNotConnected = errors.New("platform is not connected")
)

// ConnectionView is one platform on the settings screen.
type ConnectionView struct {
	Info
	Connected    bool       `json:"connected"`
	Default      bool       `json:"default"`
	Endpoint     string     `json:"endpoint,omitempty"`
	TokenLast4   string     `json:"token_last4,omitempty"`
	Status       string     `json:"status,omitempty"` // untested | ok | invalid | error
	LastTestedAt *time.Time `json:"last_tested_at,omitempty"`
	ConnectedAt  *time.Time `json:"connected_at,omitempty"`
	ConnectedBy  string     `json:"connected_by,omitempty"`
}

// ConnectionsList is the settings answer: every platform, and the org's
// default worker ("" is the shared worker).
type ConnectionsList struct {
	Platforms []ConnectionView `json:"platforms"`
	Default   string           `json:"default"`
}

// Resolved is the org's default platform with its credentials.
type Resolved struct {
	Platform    string
	Credentials Credentials
}

type storedValue struct {
	Token    string `json:"token"`
	Endpoint string `json:"endpoint,omitempty"`
}

func (s *Connections) build(name string, c Credentials) (Platform, error) {
	if s.Build != nil {
		return s.Build(name, c)
	}
	return New(name, c)
}

func (s *Connections) now() time.Time {
	if s.Now != nil {
		return s.Now()
	}
	return time.Now()
}

// find returns the keystore secret of a platform connection, or nil.
func (s *Connections) find(ctx context.Context, org, provider string) (*keystore.Key, error) {
	keys, err := s.Keys.List(ctx, org, keystore.KindSecret)
	if err != nil {
		return nil, err
	}
	for i := range keys {
		if keys[i].Provider == provider {
			return &keys[i], nil
		}
	}
	return nil, nil
}

// List returns every registered platform with the org's connection state.
func (s *Connections) List(ctx context.Context, org string) (*ConnectionsList, error) {
	keys, err := s.Keys.List(ctx, org, keystore.KindSecret)
	if err != nil {
		return nil, err
	}
	byProvider := map[string]keystore.Key{}
	for _, k := range keys {
		byProvider[k.Provider] = k
	}
	out := &ConnectionsList{Platforms: []ConnectionView{}}
	for prov := range byProvider {
		if name, ok := strings.CutPrefix(prov, providerDefault+":"); ok && IsPlatform(name) {
			if _, connected := byProvider[providerPrefix+name]; connected {
				out.Default = name
			}
		}
	}
	for _, info := range Platforms() {
		v := ConnectionView{Info: info}
		if k, ok := byProvider[providerPrefix+info.Name]; ok {
			v.Connected, v.Status, v.LastTestedAt, v.ConnectedBy = true, k.Status, k.LastTestedAt, strings.TrimPrefix(k.CreatedBy, actorPrefix)
			created := k.CreatedAt
			v.ConnectedAt = &created
			v.Endpoint = s.endpointOf(ctx, org, &k)
			v.TokenLast4 = labelLast4(k.Label)
			v.Default = out.Default == info.Name
		}
		out.Platforms = append(out.Platforms, v)
	}
	return out, nil
}

func last4(tok string) string {
	if len(tok) <= 8 {
		return ""
	}
	return tok[len(tok)-4:]
}

// Connect tests the token and, when the platform accepts it, stores it
// (replacing an earlier connection of the same platform). A token the
// platform refuses or cannot be checked is not stored.
func (s *Connections) Connect(ctx context.Context, org, name, token, endpoint, actor string) (*ConnectionView, error) {
	info, ok := Lookup(name)
	if !ok {
		return nil, fmt.Errorf("%w %q", ErrUnknownPlatform, name)
	}
	token, endpoint = strings.TrimSpace(token), strings.TrimRight(strings.TrimSpace(endpoint), "/")
	if token == "" {
		return nil, fmt.Errorf("%s: the API token is required", info.Title)
	}
	if len(endpoint) > 500 {
		return nil, fmt.Errorf("%s: %s is too long", info.Title, strings.ToLower(info.EndpointLabel))
	}
	p, err := s.build(name, Credentials{Token: token, Endpoint: endpoint})
	if err != nil {
		return nil, err
	}
	cctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	if err := p.Check(cctx); err != nil {
		return nil, fmt.Errorf("test connection failed, nothing was saved: %w", err)
	}
	raw, err := json.Marshal(storedValue{Token: token, Endpoint: endpoint})
	if err != nil {
		return nil, err
	}
	old, err := s.find(ctx, org, providerPrefix+name)
	if err != nil {
		return nil, err
	}
	// The endpoint goes in the value (it need not be a URL). The label is
	// never a valid secret name (it has spaces), so no {{secret.NAME}}
	// reference can resolve to a platform token, and the "heimdall:" actor
	// keeps it off the Secrets screen (handlers/secrets.go systemSecret).
	put := keystore.Put{Provider: providerPrefix + name, Label: connectionLabel(info.Title, token), Value: string(raw)}
	var k *keystore.Key
	if old != nil {
		k, err = s.Keys.Update(ctx, org, old.ID, put, actorPrefix+actor)
	} else {
		k, err = s.Keys.Create(ctx, org, keystore.KindSecret, put, actorPrefix+actor)
	}
	if err != nil {
		return nil, err
	}
	_ = s.Keys.SetStatus(ctx, org, k.ID, keystore.StatusOK, s.now())
	return s.view(ctx, org, name)
}

func (s *Connections) view(ctx context.Context, org, name string) (*ConnectionView, error) {
	l, err := s.List(ctx, org)
	if err != nil {
		return nil, err
	}
	for i := range l.Platforms {
		if l.Platforms[i].Name == name {
			v := l.Platforms[i]
			return &v, nil
		}
	}
	return nil, fmt.Errorf("%w %q", ErrUnknownPlatform, name)
}

func (s *Connections) endpointOf(ctx context.Context, org string, k *keystore.Key) string {
	c, err := s.credentials(ctx, org, k)
	if err != nil {
		return ""
	}
	return c.Endpoint
}

func (s *Connections) credentials(ctx context.Context, org string, k *keystore.Key) (Credentials, error) {
	raw, err := s.Keys.Reveal(ctx, org, k.ID)
	if err != nil {
		return Credentials{}, err
	}
	var v storedValue
	if err := json.Unmarshal([]byte(raw), &v); err != nil || v.Token == "" {
		return Credentials{}, errors.New("stored platform connection is unreadable; connect it again")
	}
	return Credentials{Token: v.Token, Endpoint: v.Endpoint}, nil
}

// Test checks a stored connection again and records the result.
func (s *Connections) Test(ctx context.Context, org, name string) (*ConnectionView, error) {
	if !IsPlatform(name) {
		return nil, fmt.Errorf("%w %q", ErrUnknownPlatform, name)
	}
	k, err := s.find(ctx, org, providerPrefix+name)
	if err != nil {
		return nil, err
	}
	if k == nil {
		return nil, ErrNotConnected
	}
	c, err := s.credentials(ctx, org, k)
	if err != nil {
		return nil, err
	}
	p, err := s.build(name, c)
	if err != nil {
		return nil, err
	}
	cctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	cerr := p.Check(cctx)
	status := keystore.StatusOK
	switch {
	case errors.Is(cerr, ErrUnauthorized):
		status = keystore.StatusInvalid
	case cerr != nil:
		status = keystore.StatusError
	}
	_ = s.Keys.SetStatus(ctx, org, k.ID, status, s.now())
	v, err := s.view(ctx, org, name)
	if err != nil {
		return nil, err
	}
	if cerr != nil {
		return v, cerr
	}
	return v, nil
}

// SetDefault makes a connected platform the org's default worker; "" goes
// back to the shared worker.
func (s *Connections) SetDefault(ctx context.Context, org, name, actor string) (*ConnectionsList, error) {
	if name != "" {
		if !IsPlatform(name) {
			return nil, fmt.Errorf("%w %q", ErrUnknownPlatform, name)
		}
		k, err := s.find(ctx, org, providerPrefix+name)
		if err != nil {
			return nil, err
		}
		if k == nil {
			return nil, ErrNotConnected
		}
	}
	if err := s.clearDefault(ctx, org, ""); err != nil {
		return nil, err
	}
	if name != "" {
		put := keystore.Put{Provider: providerDefault + ":" + name, Label: "worker platform default", Value: name}
		if _, err := s.Keys.Create(ctx, org, keystore.KindSecret, put, actorPrefix+actor); err != nil {
			return nil, err
		}
	}
	return s.List(ctx, org)
}

// clearDefault deletes the org's default marker; only one naming only
// (non-empty) when only is set.
func (s *Connections) clearDefault(ctx context.Context, org, only string) error {
	keys, err := s.Keys.List(ctx, org, keystore.KindSecret)
	if err != nil {
		return err
	}
	for _, k := range keys {
		name, ok := strings.CutPrefix(k.Provider, providerDefault+":")
		if !ok || (only != "" && name != only) {
			continue
		}
		if err := s.Keys.Delete(ctx, org, k.ID, Actor); err != nil && !errors.Is(err, keystore.ErrNotFound) {
			return err
		}
	}
	return nil
}

// Disconnect deletes a platform's token; a platform that was the default
// leaves the org on the shared worker.
func (s *Connections) Disconnect(ctx context.Context, org, name, actor string) (*ConnectionsList, error) {
	if !IsPlatform(name) {
		return nil, fmt.Errorf("%w %q", ErrUnknownPlatform, name)
	}
	k, err := s.find(ctx, org, providerPrefix+name)
	if err != nil {
		return nil, err
	}
	if k == nil {
		return nil, ErrNotConnected
	}
	if err := s.clearDefault(ctx, org, name); err != nil {
		return nil, err
	}
	if err := s.Keys.Delete(ctx, org, k.ID, actor); err != nil && !errors.Is(err, keystore.ErrNotFound) {
		return nil, err
	}
	return s.List(ctx, org)
}

// Resolve returns the org's default platform and its credentials, or nil
// when the org runs on the shared worker. Server internal only.
func (s *Connections) Resolve(ctx context.Context, org string) (*Resolved, error) {
	l, err := s.List(ctx, org)
	if err != nil || l.Default == "" {
		return nil, err
	}
	k, err := s.find(ctx, org, providerPrefix+l.Default)
	if err != nil || k == nil {
		return nil, err
	}
	c, err := s.credentials(ctx, org, k)
	if err != nil {
		return nil, err
	}
	return &Resolved{Platform: l.Default, Credentials: c}, nil
}

// IsPlatformSecret reports whether a keystore secret belongs to this
// package (the secrets screen may hide or label it).
func IsPlatformSecret(provider string) bool {
	return strings.HasPrefix(provider, providerPrefix) || strings.HasPrefix(provider, providerDefault+":")
}

// actorPrefix marks keystore rows this package writes for an admin.
const actorPrefix = "heimdall:worker-platforms:"

// connectionLabel is a connection's keystore label: the platform and the
// token's last four characters, with spaces, so never a secret name.
func connectionLabel(title, token string) string {
	if l4 := last4(token); l4 != "" {
		return title + " worker token ending " + l4
	}
	return title + " worker token"
}

func labelLast4(label string) string {
	if _, l4, ok := strings.Cut(label, " ending "); ok {
		return l4
	}
	return ""
}

// CredentialsOf returns the credentials of one connected platform, default
// or not (Z05: an open model served on the org's Modal account), or
// ErrNotConnected. Server internal only: the token never leaves the server.
func (s *Connections) CredentialsOf(ctx context.Context, org, name string) (Credentials, error) {
	if !IsPlatform(name) {
		return Credentials{}, fmt.Errorf("%w %q", ErrUnknownPlatform, name)
	}
	k, err := s.find(ctx, org, providerPrefix+name)
	if err != nil {
		return Credentials{}, err
	}
	if k == nil {
		return Credentials{}, fmt.Errorf("%s: %w", name, ErrNotConnected)
	}
	return s.credentials(ctx, org, k)
}
