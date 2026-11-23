package sandbox

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"

	"github.com/maximhq/bifrost/bench/keystore"
	"github.com/maximhq/bifrost/bench/utils"
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
	// RegistryClient signs in to an image registry when its login is
	// saved; nil uses a client that reaches public addresses only.
	RegistryClient *http.Client
}

// Keystore markers.
const (
	providerPrefix  = "worker-platform:"
	providerDefault = "worker-platform-default"
	// providerRegistry marks a platform's image registry login (fix wave
	// 3, N216): one per platform, beside its token.
	providerRegistry = "worker-platform-registry:"
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
	// Registry is the image registry the platform pulls Heimdall's images
	// from (fix wave 3, N216); nil when none is saved. Its token is never
	// returned.
	Registry *RegistryView `json:"registry,omitempty"`
}

// RegistryView is a saved image registry login without its token.
type RegistryView struct {
	Registry   string     `json:"registry"`
	Username   string     `json:"username"`
	TokenLast4 string     `json:"token_last4,omitempty"`
	TestedAt   *time.Time `json:"tested_at,omitempty"`
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
	// Registry is the platform's image registry login, nil when none is
	// saved (fix wave 3, N216).
	Registry *ImageRegistry
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
			if rk, ok := byProvider[providerRegistry+info.Name]; ok {
				v.Registry = s.registryView(ctx, org, &rk)
			}
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
	if err := s.RemoveRegistry(ctx, org, name, actor); err != nil && !errors.Is(err, ErrNotConnected) {
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
	reg, err := s.RegistryOf(ctx, org, l.Default)
	if err != nil {
		return nil, err
	}
	return &Resolved{Platform: l.Default, Credentials: c, Registry: reg}, nil
}

// IsPlatformSecret reports whether a keystore secret belongs to this
// package (the secrets screen may hide or label it).
func IsPlatformSecret(provider string) bool {
	return strings.HasPrefix(provider, providerPrefix) || strings.HasPrefix(provider, providerDefault+":") ||
		strings.HasPrefix(provider, providerRegistry)
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

// Image registry logins (fix wave 3, N216 fix 2). A sandbox platform in the
// cloud pulls Heimdall's images from the org's own registry: the shared
// runner pushes each image there with this login and the platform pulls it
// with the same login. The server only signs in to the registry's v2 API
// when the login is saved; it never runs docker and never pushes, so push
// rights are proven on first use.

// Registry login errors.
var (
	// ErrRegistryPath refuses a registry that is not host/path.
	ErrRegistryPath = errors.New("Use a registry path like ghcr.io/team/heimdall.")
	// ErrRegistryInput refuses a login without a user name or token, or for
	// a platform that does not pull from a registry with a login.
	ErrRegistryInput = errors.New("registry login")
)

// RegistrySignInError is a registry that refused or could not check the
// login, in plain words.
type RegistrySignInError struct {
	Registry, Reason string
}

func (e *RegistrySignInError) Error() string {
	return "Heimdall could not sign in to " + e.Registry + ": " + e.Reason + "."
}

var (
	registryHostRe = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]*[a-z0-9])?(\.[a-z0-9]([a-z0-9-]*[a-z0-9])?)*(:[0-9]{1,5})?$`)
	registryPathRe = regexp.MustCompile(`^[a-z0-9]+([._-][a-z0-9]+)*$`)
)

// NormalizeRegistryPath checks a registry path as a person typed it
// (ghcr.io/team/heimdall): a host with a dot or a port, then one or more
// lower case path parts. A scheme and a trailing slash are dropped.
func NormalizeRegistryPath(raw string) (string, error) {
	p := strings.TrimSpace(raw)
	p = strings.TrimPrefix(strings.TrimPrefix(p, "https://"), "http://")
	p = strings.Trim(p, "/")
	host, rest, ok := strings.Cut(p, "/")
	if !ok || rest == "" || len(p) > 200 || !registryHostRe.MatchString(host) || !strings.ContainsAny(host, ".:") {
		return "", ErrRegistryPath
	}
	for _, part := range strings.Split(rest, "/") {
		if !registryPathRe.MatchString(part) {
			return "", ErrRegistryPath
		}
	}
	return p, nil
}

func registryLabel(title, token string) string {
	if l4 := last4(token); l4 != "" {
		return title + " image registry token ending " + l4
	}
	return title + " image registry token"
}

// registryView reads a stored login without returning its token.
func (s *Connections) registryView(ctx context.Context, org string, k *keystore.Key) *RegistryView {
	r, err := s.registryValue(ctx, org, k)
	if err != nil {
		return nil
	}
	return &RegistryView{Registry: r.Registry, Username: r.Username, TokenLast4: labelLast4(k.Label), TestedAt: k.LastTestedAt}
}

func (s *Connections) registryValue(ctx context.Context, org string, k *keystore.Key) (*ImageRegistry, error) {
	raw, err := s.Keys.Reveal(ctx, org, k.ID)
	if err != nil {
		return nil, err
	}
	var r ImageRegistry
	if err := json.Unmarshal([]byte(raw), &r); err != nil || r.Registry == "" || r.Token == "" {
		return nil, errors.New("stored image registry login is unreadable; save it again")
	}
	return &r, nil
}

// RegistryOf returns a platform's saved image registry login with its
// token, or nil when none is saved. Server internal only.
func (s *Connections) RegistryOf(ctx context.Context, org, name string) (*ImageRegistry, error) {
	if !IsPlatform(name) {
		return nil, fmt.Errorf("%w %q", ErrUnknownPlatform, name)
	}
	k, err := s.find(ctx, org, providerRegistry+name)
	if err != nil || k == nil {
		return nil, err
	}
	return s.registryValue(ctx, org, k)
}

// SaveRegistry signs in to the registry with the login and, when the
// registry accepts it, stores it beside the platform's token (replacing an
// earlier one). Only a connected Modal takes a registry login.
func (s *Connections) SaveRegistry(ctx context.Context, org, name, registry, username, token, actor string) (*RegistryView, error) {
	info, ok := Lookup(name)
	if !ok {
		return nil, fmt.Errorf("%w %q", ErrUnknownPlatform, name)
	}
	if name != PlatformModal {
		return nil, fmt.Errorf("%w: %s does not pull images from your registry", ErrRegistryInput, info.Title)
	}
	path, err := NormalizeRegistryPath(registry)
	if err != nil {
		return nil, err
	}
	username, token = strings.TrimSpace(username), strings.TrimSpace(token)
	if username == "" || token == "" {
		return nil, fmt.Errorf("%w: the user name and the token are required", ErrRegistryInput)
	}
	pk, err := s.find(ctx, org, providerPrefix+name)
	if err != nil {
		return nil, err
	}
	if pk == nil {
		return nil, ErrNotConnected
	}
	login := ImageRegistry{Registry: path, Username: username, Token: token}
	cctx, cancel := context.WithTimeout(ctx, time.Minute)
	defer cancel()
	if err := CheckRegistrySignIn(cctx, s.RegistryClient, login); err != nil {
		return nil, err
	}
	raw, err := json.Marshal(login)
	if err != nil {
		return nil, err
	}
	old, err := s.find(ctx, org, providerRegistry+name)
	if err != nil {
		return nil, err
	}
	put := keystore.Put{Provider: providerRegistry + name, Label: registryLabel(info.Title, token), Value: string(raw)}
	var k *keystore.Key
	if old != nil {
		k, err = s.Keys.Update(ctx, org, old.ID, put, actorPrefix+actor)
	} else {
		k, err = s.Keys.Create(ctx, org, keystore.KindSecret, put, actorPrefix+actor)
	}
	if err != nil {
		return nil, err
	}
	now := s.now()
	_ = s.Keys.SetStatus(ctx, org, k.ID, keystore.StatusOK, now)
	return &RegistryView{Registry: path, Username: username, TokenLast4: last4(token), TestedAt: &now}, nil
}

// RemoveRegistry deletes a platform's image registry login;
// ErrNotConnected when none is saved.
func (s *Connections) RemoveRegistry(ctx context.Context, org, name, actor string) error {
	if !IsPlatform(name) {
		return fmt.Errorf("%w %q", ErrUnknownPlatform, name)
	}
	k, err := s.find(ctx, org, providerRegistry+name)
	if err != nil {
		return err
	}
	if k == nil {
		return ErrNotConnected
	}
	if err := s.Keys.Delete(ctx, org, k.ID, actor); err != nil && !errors.Is(err, keystore.ErrNotFound) {
		return err
	}
	return nil
}

// registrySignInClient reaches public addresses only (utils.GuardedTransport).
func registrySignInClient() *http.Client {
	return &http.Client{Transport: utils.GuardedTransport(), Timeout: 30 * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
}

// CheckRegistrySignIn signs in to the registry's v2 API with the login:
// GET https://<host>/v2/ with basic auth and, when the registry answers 401
// with a Bearer challenge, the token flow at its realm. 200 means signed in.
// A nil client reaches public addresses only. Errors are
// *RegistrySignInError in plain words and never carry the token.
func CheckRegistrySignIn(ctx context.Context, client *http.Client, r ImageRegistry) error {
	if client == nil {
		client = registrySignInClient()
	}
	fail := func(reason string) error { return &RegistrySignInError{Registry: r.Registry, Reason: reason} }
	host := r.Host()
	if host == "docker.io" {
		host = "registry-1.docker.io"
	}
	base := "https://" + host + "/v2/"
	resp, err := registryGet(ctx, client, base, func(req *http.Request) { req.SetBasicAuth(r.Username, r.Token) })
	if err != nil {
		return fail("it could not be reached")
	}
	switch resp.StatusCode {
	case http.StatusOK:
		return nil
	case http.StatusUnauthorized, http.StatusForbidden:
	default:
		return fail(fmt.Sprintf("it answered HTTP %d", resp.StatusCode))
	}
	realm, params := bearerChallenge(resp.Header.Get("WWW-Authenticate"))
	if realm == "" {
		return fail("the user name or token was refused")
	}
	ru, err := url.Parse(realm)
	if err != nil || ru.Scheme != "https" {
		return fail("its sign-in address is not https")
	}
	q := ru.Query()
	if v := params["service"]; v != "" {
		q.Set("service", v)
	}
	scope := params["scope"]
	if scope == "" {
		if _, repo, ok := strings.Cut(r.Registry, "/"); ok {
			scope = "repository:" + repo + "/heimdall-check:push,pull"
		}
	}
	if scope != "" {
		q.Set("scope", scope)
	}
	ru.RawQuery = q.Encode()
	tr, err := registryGet(ctx, client, ru.String(), func(req *http.Request) { req.SetBasicAuth(r.Username, r.Token) })
	if err != nil {
		return fail("its sign-in service could not be reached")
	}
	if tr.StatusCode == http.StatusUnauthorized || tr.StatusCode == http.StatusForbidden {
		return fail("the user name or token was refused")
	}
	if tr.StatusCode != http.StatusOK {
		return fail(fmt.Sprintf("its sign-in service answered HTTP %d", tr.StatusCode))
	}
	var tok struct {
		Token       string `json:"token"`
		AccessToken string `json:"access_token"`
	}
	if json.Unmarshal(tr.body, &tok) != nil || tok.Token == "" && tok.AccessToken == "" {
		return fail("its sign-in service gave no token")
	}
	bearer := tok.Token
	if bearer == "" {
		bearer = tok.AccessToken
	}
	again, err := registryGet(ctx, client, base, func(req *http.Request) { req.Header.Set("Authorization", "Bearer "+bearer) })
	if err != nil {
		return fail("it could not be reached")
	}
	switch again.StatusCode {
	case http.StatusOK:
		return nil
	case http.StatusUnauthorized, http.StatusForbidden:
		return fail("the user name or token was refused")
	}
	return fail(fmt.Sprintf("it answered HTTP %d", again.StatusCode))
}

type registryAnswer struct {
	StatusCode int
	Header     http.Header
	body       []byte
}

func registryGet(ctx context.Context, client *http.Client, u string, set func(*http.Request)) (*registryAnswer, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, err
	}
	set(req)
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
	return &registryAnswer{StatusCode: resp.StatusCode, Header: resp.Header, body: b}, nil
}

// bearerChallenge parses `Bearer realm="...",service="...",scope="..."`.
func bearerChallenge(h string) (string, map[string]string) {
	h = strings.TrimSpace(h)
	if len(h) < 7 || !strings.EqualFold(h[:7], "bearer ") {
		return "", nil
	}
	params := map[string]string{}
	rest := h[7:]
	for rest != "" {
		rest = strings.TrimLeft(rest, " ,")
		k, v, ok := strings.Cut(rest, "=")
		if !ok {
			break
		}
		k = strings.ToLower(strings.TrimSpace(k))
		v = strings.TrimSpace(v)
		if strings.HasPrefix(v, `"`) {
			end := strings.Index(v[1:], `"`)
			if end < 0 {
				params[k] = v[1:]
				break
			}
			params[k], rest = v[1:end+1], v[end+2:]
			continue
		}
		val, next, _ := strings.Cut(v, ",")
		params[k], rest = strings.TrimSpace(val), next
	}
	return params["realm"], params
}
