// Package sharedpool is the hosted platform's shared runner pool and its
// guardrails (web parity lane L03, F3):
//
//   - every org with quota.Limits.SharedPool gets a runner token in pool
//     "shared", minted on demand and kept in the org's keystore (Kind
//     secret); the shared runner fetches the list of assignments with
//     HEIMDALL_SHARED_RUNNER_SECRET and runs one worker loop per org;
//   - the shared runner runs only what its own project reproduces: the
//     server's built-in templates. Custom Docker images need a request
//     that a server admin approves; images must be pinned by digest
//     (name@sha256:...) and fit the org's MaxImageBytes;
//   - the platform kill switch empties the assignments and blocks new
//     leases (the coordinator gate lives in cmd/bifrost-bench).
package sharedpool

import (
	"context"
	"crypto/subtle"
	"errors"
	"fmt"
	"os"
	"regexp"
	"strings"
	"sync"

	"github.com/maximhq/bifrost/bench/keystore"
	"github.com/maximhq/bifrost/bench/quota"
	"github.com/maximhq/bifrost/bench/schemas"
)

// Names.
const (
	// Pool is the runner pool of the shared runners.
	Pool = "shared"
	// SecretEnv holds the secret the shared runner presents to fetch its
	// assignments. Unset, the shared pool is off.
	SecretEnv = "HEIMDALL_SHARED_RUNNER_SECRET"
	// TokenLabel and TokenProvider mark the keystore secret that holds an
	// org's shared pool runner token.
	TokenLabel    = "shared-runner-token"
	TokenProvider = "heimdall-shared-pool"
	// TokenName is the runner token's name in the org's token list.
	TokenName = "heimdall shared pool"
	// Actor is the audit actor of tokens and secrets this package writes.
	Actor = "heimdall:shared-pool"
)

// Errors.
var (
	ErrImageRef     = errors.New("image must be pinned by digest: name@sha256:<64 hex characters>")
	ErrImageTooBig  = errors.New("image is larger than the organization's image size limit")
	ErrNotPending   = errors.New("image request was already decided")
	ErrNotFound     = errors.New("image request not found")
	ErrDisabled     = errors.New("shared runner pool is off: set " + SecretEnv + " on the server")
	ErrUnauthorized = errors.New("shared runner secret missing or wrong")
)

var (
	digestRe    = regexp.MustCompile(`^[a-f0-9]{64}$`)
	imageNameRe = regexp.MustCompile(`^[a-z0-9]+([._/:-][a-z0-9]+)*$`)
)

// CheckImageRef validates a digest-pinned image reference and returns the
// image name and the digest (sha256:<hex>). A tag before the digest is
// allowed (name:tag@sha256:...); a bare tag is refused.
func CheckImageRef(ref string) (name, digest string, err error) {
	ref = strings.TrimSpace(ref)
	if ref == "" || len(ref) > 512 || strings.ContainsAny(ref, " \t\r\n") {
		return "", "", ErrImageRef
	}
	at := strings.LastIndex(ref, "@")
	if at <= 0 {
		return "", "", ErrImageRef
	}
	name, digest = ref[:at], ref[at+1:]
	hex, ok := strings.CutPrefix(digest, "sha256:")
	if !ok || !digestRe.MatchString(hex) || !imageNameRe.MatchString(strings.ToLower(name)) || name != strings.ToLower(name) {
		return "", "", ErrImageRef
	}
	return name, digest, nil
}

// CheckImageSize refuses an image above the org's limit (0: no limit).
func CheckImageSize(size int64, l quota.Limits) error {
	if size < 0 {
		return fmt.Errorf("%w: size must not be negative", ErrImageTooBig)
	}
	if l.MaxImageBytes > 0 && size > l.MaxImageBytes {
		return fmt.Errorf("%w (%d > %d bytes)", ErrImageTooBig, size, l.MaxImageBytes)
	}
	return nil
}

// Secret is the configured shared runner secret ("" when the pool is off).
func Secret() string { return strings.TrimSpace(os.Getenv(SecretEnv)) }

// CheckSecret compares a presented secret with the configured one in
// constant time.
func CheckSecret(configured, presented string) error {
	if configured == "" {
		return ErrDisabled
	}
	if presented == "" || subtle.ConstantTimeCompare([]byte(configured), []byte(presented)) != 1 {
		return ErrUnauthorized
	}
	return nil
}

// Minter mints and revokes runner tokens (runners.Service).
type Minter interface {
	CreateToken(ctx context.Context, orgID, createdBy string, req schemas.RunnerTokenCreate) (*schemas.RunnerTokenCreated, error)
	ListTokens(ctx context.Context, orgID string) ([]schemas.RunnerTokenView, error)
	RevokeToken(ctx context.Context, orgID, id string) (*schemas.RunnerTokenView, error)
}

// Assignment is one org the shared runner serves.
type Assignment struct {
	OrgID             string   `json:"org_id"`
	Pool              string   `json:"pool"`
	Token             string   `json:"token"`
	Runtime           string   `json:"runtime"`
	MaxConcurrentRuns int      `json:"max_concurrent_runs"`
	Images            []string `json:"images,omitempty"` // approved custom images
}

// Assignments is the answer of GET /api/bench/admin/pool/assignments.
type Assignments struct {
	KillSwitch  bool         `json:"kill_switch"`
	Assignments []Assignment `json:"assignments"`
}

// Service builds the shared runner's assignments.
type Service struct {
	Quota  quota.Store
	Keys   keystore.Store
	Minter Minter
	Images *ImageStore // nil: no custom images
	// Orgs lists every org id.
	Orgs func(ctx context.Context) ([]string, error)

	mu sync.Mutex // one mint at a time
}

// Assignments lists the orgs the shared runner serves. runtime, when set,
// keeps only orgs whose limits ask for it (one shared runner per runtime).
// With the kill switch on, the list is empty.
func (s *Service) Assignments(ctx context.Context, runtime string) (*Assignments, error) {
	if s.Quota == nil || s.Keys == nil || s.Minter == nil || s.Orgs == nil {
		return nil, ErrDisabled
	}
	out := &Assignments{Assignments: []Assignment{}}
	kill, err := s.Quota.KillSwitch(ctx)
	if err != nil {
		return nil, err
	}
	if kill {
		out.KillSwitch = true
		return out, nil
	}
	orgs, err := s.Orgs(ctx)
	if err != nil {
		return nil, err
	}
	for _, org := range orgs {
		l, err := s.Quota.Get(ctx, org)
		if err != nil {
			return nil, err
		}
		l = quota.Normalize(l)
		if !l.SharedPool || (runtime != "" && l.Runtime != runtime) {
			continue
		}
		tok, err := s.EnsureToken(ctx, org)
		if err != nil {
			return nil, fmt.Errorf("org %s: %w", org, err)
		}
		a := Assignment{OrgID: org, Pool: Pool, Token: tok, Runtime: l.Runtime, MaxConcurrentRuns: l.MaxConcurrentRuns}
		if s.Images != nil && l.AllowCustomImages {
			if a.Images, err = s.Images.Approved(ctx, org); err != nil {
				return nil, err
			}
		}
		out.Assignments = append(out.Assignments, a)
	}
	return out, nil
}

// EnsureToken returns the org's shared pool runner token, minting one
// (and revoking stale shared pool tokens) when the keystore has none.
func (s *Service) EnsureToken(ctx context.Context, orgID string) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	keys, err := s.Keys.List(ctx, orgID, keystore.KindSecret)
	if err != nil {
		return "", err
	}
	for _, k := range keys {
		if k.Label == TokenLabel && k.Provider == TokenProvider {
			if v, err := s.Keys.Reveal(ctx, orgID, k.ID); err == nil && v != "" {
				return v, nil
			}
			_ = s.Keys.Delete(ctx, orgID, k.ID, Actor) // unreadable: replace it
		}
	}
	old, err := s.Minter.ListTokens(ctx, orgID)
	if err != nil {
		return "", err
	}
	for _, t := range old {
		if t.Pool == Pool && t.Name == TokenName && t.RevokedAt == nil {
			if _, err := s.Minter.RevokeToken(ctx, orgID, t.ID); err != nil {
				return "", err
			}
		}
	}
	created, err := s.Minter.CreateToken(ctx, orgID, Actor, schemas.RunnerTokenCreate{Pool: Pool, Name: TokenName})
	if err != nil {
		return "", err
	}
	if _, err := s.Keys.Create(ctx, orgID, keystore.KindSecret, keystore.Put{Provider: TokenProvider, Label: TokenLabel, Value: created.Token}, Actor); err != nil {
		_, _ = s.Minter.RevokeToken(ctx, orgID, created.Info.ID)
		return "", err
	}
	return created.Token, nil
}
