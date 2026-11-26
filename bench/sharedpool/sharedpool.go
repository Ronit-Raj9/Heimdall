// Package sharedpool is the hosted platform's shared runner pool and its
// guardrails (web parity lane L03, F3):
//
//   - every org with quota.Limits.SharedPool is assigned to the shared
//     runner, which fetches the list (no tokens in it) with
//     HEIMDALL_SHARED_RUNNER_SECRET, claims one runner token per org in pool
//     "shared" (a claim revokes the org's earlier token; the server keeps
//     only hashes), and runs one worker loop per org;
//   - the shared runner runs only what its own project reproduces: the
//     server's built-in templates. Custom Docker images need a request
//     that a server admin approves; images must be pinned by digest
//     (name@sha256:...) and fit the org's MaxImageBytes;
//   - the platform kill switch empties the assignments and blocks new
//     leases (the coordinator gate lives in cmd/bifrost-bench).
package sharedpool

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/maximhq/bifrost/bench/keystore"
	"github.com/maximhq/bifrost/bench/quota"
	"github.com/maximhq/bifrost/bench/schemas"
)

// Names.
const (
	// Pool is the runner pool of the shared runners.
	Pool = schemas.SharedRunnerPool
	// SecretEnv holds the secret the shared runner presents to fetch its
	// assignments. Unset, the shared pool is off.
	SecretEnv = "HEIMDALL_SHARED_RUNNER_SECRET"
	// TokenLabel and TokenProvider mark the keystore secret in which older
	// servers kept an org's shared pool runner token (now removed on claim).
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

// Assignment is one org the shared runner serves. It carries no token: the
// runner claims each org's token once (ClaimToken) and compares TokenID to
// notice a revoked or rotated token (PROD2 P0 isolation (c)).
type Assignment struct {
	OrgID             string   `json:"org_id"`
	Pool              string   `json:"pool"`
	TokenID           string   `json:"token_id,omitempty"` // the org's live shared pool token ("" before the first claim)
	Runtime           string   `json:"runtime"`
	MaxConcurrentRuns int      `json:"max_concurrent_runs"`
	Images            []string `json:"images,omitempty"` // approved custom images (digest pinned); the runner pulls only these
	// Platform is the org's own cloud sandbox platform when it chose one as
	// its default worker (bench/sandbox): the shared runner then runs the
	// org's runs in that platform instead of on its own Docker daemon. In
	// the assignment list it carries no token, only the platform's name,
	// endpoint and Stamp; the token comes with the claim (ClaimedToken) and
	// the runner keeps it in memory only.
	Platform *PlatformAssignment `json:"platform,omitempty"`
	// Platforms are the org's connected sandbox platforms with an image
	// registry login (N208): each takes single evaluations in its own pool
	// (PlatformPool), served by a worker of its own. Public views, stamped.
	Platforms []*PlatformAssignment `json:"platforms,omitempty"`
}

// PlatformPool is the runner pool of evaluations that run on the sandbox
// platform name (N208). Pool names may not hold "/", so the parts are
// joined with "-".
func PlatformPool(name string) string { return "shared-platform-" + name }

// PlatformClaim is one per-evaluation platform of a claim: the platform
// with its credentials and the runner token minted for its pool.
type PlatformClaim struct {
	Platform *PlatformAssignment `json:"platform"`
	TokenID  string              `json:"token_id"`
	Token    string              `json:"token"`
}

// platformTokenName is the name of the runner token of platform name's pool.
func platformTokenName(name string) string { return TokenName + "-" + name }

// PlatformAssignment is an org's default sandbox platform and credentials.
type PlatformAssignment struct {
	Name     string `json:"name"`
	Token    string `json:"token,omitempty"` // only in a ClaimedToken
	Endpoint string `json:"endpoint,omitempty"`
	// Stamp changes when the token or endpoint does (a short hash, never the
	// token), so a runner restarts the org's loop on a rotated credential.
	Stamp string `json:"stamp,omitempty"`
	// Registry is the org's own image registry the platform pulls
	// Heimdall's images from (fix wave 3, N216); nil when none is saved.
	// Its token is only in a ClaimedToken.
	Registry *RegistryLogin `json:"registry,omitempty"`
}

// RegistryLogin is an org's image registry login: the registry path images
// are pushed under (ghcr.io/team/heimdall), a user name and a token with
// push and pull rights.
type RegistryLogin struct {
	Registry string `json:"registry"`
	Username string `json:"username"`
	Token    string `json:"token,omitempty"` // only in a ClaimedToken
}

// publicView is p without its tokens, stamped.
func (p *PlatformAssignment) publicView() *PlatformAssignment {
	if p == nil {
		return nil
	}
	seed := p.Token + "\x00" + p.Endpoint
	var reg *RegistryLogin
	if p.Registry != nil {
		seed += "\x00" + p.Registry.Registry + "\x00" + p.Registry.Username + "\x00" + p.Registry.Token
		reg = &RegistryLogin{Registry: p.Registry.Registry, Username: p.Registry.Username}
	}
	sum := sha256.Sum256([]byte(seed))
	return &PlatformAssignment{Name: p.Name, Endpoint: p.Endpoint, Stamp: hex.EncodeToString(sum[:8]), Registry: reg}
}

// Assignments is the answer of GET /api/bench/admin/pool/assignments.
type Assignments struct {
	KillSwitch  bool         `json:"kill_switch"`
	Assignments []Assignment `json:"assignments"`
}

// ClaimedToken is the answer of POST /api/bench/admin/pool/assignments/{org}/token:
// a fresh runner token for one org, shown once.
type ClaimedToken struct {
	OrgID   string `json:"org_id"`
	TokenID string `json:"token_id"`
	Token   string `json:"token"`
	// Platform is the org's sandbox platform with its token, when it chose
	// one (Assignment.Platform); nil runs on the runner's own Docker.
	Platform *PlatformAssignment `json:"platform,omitempty"`
	// Platforms are the per-evaluation platforms (Assignment.Platforms),
	// each with its own runner token.
	Platforms []PlatformClaim `json:"platforms,omitempty"`
}

// ErrNotAssigned refuses a token claim for an org the shared pool does not serve.
var ErrNotAssigned = errors.New("organization is not on the shared worker")

// Service builds the shared runner's assignments.
type Service struct {
	Quota  quota.Store
	Keys   keystore.Store // optional: only to remove token copies older servers kept
	Minter Minter
	Images *ImageStore // nil: no custom images
	// Orgs lists every org id.
	Orgs func(ctx context.Context) ([]string, error)
	// Platform returns an org's default sandbox platform, nil when the org
	// runs on the shared worker. An org with one is served even without the
	// shared pool: the compute is its own. nil: no platforms.
	Platform func(ctx context.Context, orgID string) (*PlatformAssignment, error)
	// Platforms returns every connected sandbox platform of an org with an
	// image registry login, with credentials (N208). An org with one is
	// served even without the shared pool. nil: none.
	Platforms func(ctx context.Context, orgID string) ([]*PlatformAssignment, error)

	mu sync.Mutex // one claim at a time

	// Now is the clock of the gVisor observation (nil: time.Now).
	Now func() time.Time

	rtMu     sync.Mutex
	gvisorAt time.Time // last assignments call from a shared worker running gVisor
}

// GVisorWindow is how long one assignments call from a shared worker that
// runs gVisor counts as "the shared worker runs gVisor".
const GVisorWindow = 10 * time.Minute

// AutoApproveEnv turns automatic image approval off when set to "off".
const AutoApproveEnv = "HEIMDALL_IMAGE_AUTO_APPROVE"

// AutoApproveOn reports whether automatic image approval is on (default).
func AutoApproveOn() bool {
	v := strings.ToLower(strings.TrimSpace(os.Getenv(AutoApproveEnv)))
	return v != "off" && v != "false" && v != "0"
}

func (s *Service) now() time.Time {
	if s.Now != nil {
		return s.Now()
	}
	return time.Now()
}

// noteRuntime records the assignment filter of a shared worker: "" (every
// org, which a worker asks for only when it runs gVisor) or runsc.
func (s *Service) noteRuntime(filter string) {
	if filter == quota.RuntimeRunc {
		return
	}
	s.rtMu.Lock()
	s.gvisorAt = s.now()
	s.rtMu.Unlock()
}

// GVisor reports whether a shared worker running every container under
// gVisor asked for its assignments within GVisorWindow.
func (s *Service) GVisor() bool {
	if s == nil {
		return false
	}
	s.rtMu.Lock()
	defer s.rtMu.Unlock()
	return !s.gvisorAt.IsZero() && s.now().Sub(s.gvisorAt) < GVisorWindow
}

// AutoApprovable reports whether a request of an org with limits l may be
// approved without a server admin: automatic approval is on, the image is
// pinned by digest, it fits the size limit (when one is set), and the
// org's runs on the shared worker run under gVisor (the org's runtime is
// runsc, or gvisor: a shared worker running gVisor serves every org). The
// reason says why not, in plain words, when it may not.
func AutoApprovable(l quota.Limits, image string, size int64, gvisor bool) (bool, string) {
	switch {
	case !AutoApproveOn():
		return false, "automatic approval is off on this server"
	case !gvisor && l.Runtime != quota.RuntimeRunsc:
		return false, "the shared worker does not run agents under gVisor"
	}
	if _, _, err := CheckImageRef(image); err != nil {
		return false, err.Error()
	}
	if err := CheckImageSize(size, l); err != nil {
		return false, err.Error()
	}
	return true, ""
}

// AutoApproveReason is the decision recorded on an automatic approval.
const AutoApproveReason = "approved automatically: pinned by digest, within the size limit, runs under gVisor"

// assigned returns the org's normalized limits, its sandbox platform (nil
// when it runs on the shared worker), its per-evaluation platforms and
// whether the shared pool serves it for runtime ("" any). An org with its
// own platform, default or per evaluation, is served even without the
// shared pool: the compute is its own.
func (s *Service) assigned(ctx context.Context, org, runtime string) (quota.Limits, *PlatformAssignment, []*PlatformAssignment, bool, error) {
	l, err := s.Quota.Get(ctx, org)
	if err != nil {
		return l, nil, nil, false, err
	}
	l = quota.Normalize(l)
	var plat *PlatformAssignment
	if s.Platform != nil {
		if plat, err = s.Platform(ctx, org); err != nil {
			return l, nil, nil, false, fmt.Errorf("org %s: worker platform: %w", org, err)
		}
	}
	var plats []*PlatformAssignment
	if s.Platforms != nil {
		if plats, err = s.Platforms(ctx, org); err != nil {
			return l, nil, nil, false, fmt.Errorf("org %s: worker platforms: %w", org, err)
		}
	}
	return l, plat, plats, (l.SharedPool || plat != nil || len(plats) > 0) && (runtime == "" || l.Runtime == runtime), nil
}

// liveTokenID is the id of the org's unrevoked shared pool token ("" none).
func (s *Service) liveTokenID(ctx context.Context, org string) (string, error) {
	toks, err := s.Minter.ListTokens(ctx, org)
	if err != nil {
		return "", err
	}
	id := ""
	for _, t := range toks {
		if t.Pool == Pool && t.Name == TokenName && t.RevokedAt == nil {
			id = t.ID
		}
	}
	return id, nil
}

// Assignments lists the orgs the shared runner serves. runtime, when set,
// keeps only orgs whose limits ask for it (one shared runner per runtime).
// With the kill switch on, the list is empty. No token is in the answer.
func (s *Service) Assignments(ctx context.Context, runtime string) (*Assignments, error) {
	if s.Quota == nil || s.Minter == nil || s.Orgs == nil {
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
	s.noteRuntime(runtime)
	gvisor := runtime != quota.RuntimeRunc // "" is asked only by a worker running gVisor
	for _, org := range orgs {
		l, plat, plats, ok, err := s.assigned(ctx, org, runtime)
		if err != nil {
			return nil, err
		}
		if !ok {
			continue
		}
		id, err := s.liveTokenID(ctx, org)
		if err != nil {
			return nil, fmt.Errorf("org %s: %w", org, err)
		}
		a := Assignment{OrgID: org, Pool: Pool, TokenID: id, Runtime: l.Runtime, MaxConcurrentRuns: l.MaxConcurrentRuns, Platform: plat.publicView()}
		for _, p := range plats {
			a.Platforms = append(a.Platforms, p.publicView())
		}
		if s.Images != nil && gvisor {
			if err := s.autoApprovePending(ctx, org, l); err != nil {
				return nil, err
			}
			if l, err = s.Quota.Get(ctx, org); err != nil {
				return nil, err
			}
			l = quota.Normalize(l)
		}
		if s.Images != nil && l.AllowCustomImages {
			if a.Images, err = s.Images.ApprovedFor(ctx, org, gvisor); err != nil {
				return nil, err
			}
		}
		out.Assignments = append(out.Assignments, a)
	}
	return out, nil
}

// autoApprovePending approves the org's pending requests that qualify for
// automatic approval, now that a shared worker running gVisor serves it
// (requests filed while none was seen, or before a server restart).
func (s *Service) autoApprovePending(ctx context.Context, org string, l quota.Limits) error {
	pending, err := s.Images.List(ctx, org, StatusPending)
	if err != nil {
		return err
	}
	for _, r := range pending {
		if ok, _ := AutoApprovable(l, r.Image, r.SizeBytes, true); !ok {
			continue
		}
		if _, err := s.Images.AutoApprove(ctx, r.ID, AutoApproveReason); err != nil && !errors.Is(err, ErrNotPending) {
			return err
		}
		if err := s.AllowImages(ctx, org); err != nil {
			return err
		}
	}
	return nil
}

// AllowImages turns custom images on for an org (an approved image is of
// no use otherwise, J6). It changes nothing when they are already on.
func (s *Service) AllowImages(ctx context.Context, org string) error {
	l, err := s.Quota.Get(ctx, org)
	if err != nil {
		return err
	}
	if l.AllowCustomImages {
		return nil
	}
	l.AllowCustomImages = true
	return s.Quota.Set(ctx, org, l, Actor)
}

// ClaimToken mints a fresh shared pool runner token for one assigned org
// and revokes the org's earlier ones, so exactly one live token exists per
// org and none is kept in readable form: the server stores only its hash.
// A second claim (another holder of the pool secret, or a restarted
// runner) revokes the first, which the serving runner notices through the
// assignment's TokenID; every claim is audited by the caller.
func (s *Service) ClaimToken(ctx context.Context, org, runtime string) (*ClaimedToken, error) {
	if s.Quota == nil || s.Minter == nil {
		return nil, ErrDisabled
	}
	if kill, err := s.Quota.KillSwitch(ctx); err != nil {
		return nil, err
	} else if kill {
		return nil, ErrNotAssigned
	}
	_, plat, plats, ok, err := s.assigned(ctx, org, runtime)
	if err != nil {
		return nil, err
	} else if !ok {
		return nil, ErrNotAssigned
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	old, err := s.Minter.ListTokens(ctx, org)
	if err != nil {
		return nil, err
	}
	revoke := map[[2]string]bool{{Pool, TokenName}: true}
	for _, p := range plats {
		revoke[[2]string{PlatformPool(p.Name), platformTokenName(p.Name)}] = true
	}
	for _, t := range old {
		if revoke[[2]string{t.Pool, t.Name}] && t.RevokedAt == nil {
			if _, err := s.Minter.RevokeToken(ctx, org, t.ID); err != nil {
				return nil, err
			}
		}
	}
	s.dropLegacyCopies(ctx, org)
	created, err := s.Minter.CreateToken(ctx, org, Actor, schemas.RunnerTokenCreate{Pool: Pool, Name: TokenName})
	if err != nil {
		return nil, err
	}
	if plat != nil {
		stamped := *plat
		stamped.Stamp = plat.publicView().Stamp
		plat = &stamped
	}
	out := &ClaimedToken{OrgID: org, TokenID: created.Info.ID, Token: created.Token, Platform: plat}
	for _, p := range plats {
		if p == nil {
			continue
		}
		pc, err := s.Minter.CreateToken(ctx, org, Actor, schemas.RunnerTokenCreate{Pool: PlatformPool(p.Name), Name: platformTokenName(p.Name)})
		if err != nil {
			return nil, err
		}
		stamped := *p
		stamped.Stamp = p.publicView().Stamp
		out.Platforms = append(out.Platforms, PlatformClaim{Platform: &stamped, TokenID: pc.Info.ID, Token: pc.Token})
	}
	return out, nil
}

// dropLegacyCopies deletes the readable token copies older servers kept in
// the org's keystore.
func (s *Service) dropLegacyCopies(ctx context.Context, org string) {
	if s.Keys == nil {
		return
	}
	keys, err := s.Keys.List(ctx, org, keystore.KindSecret)
	if err != nil {
		return
	}
	for _, k := range keys {
		if k.Label == TokenLabel && k.Provider == TokenProvider {
			_ = s.Keys.Delete(ctx, org, k.ID, Actor)
		}
	}
}
