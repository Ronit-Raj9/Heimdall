// Package runners owns the hosted platform's runners and runner tokens
// (docs/runners.md, docs/platform-design.md "Runners"): the runners and
// runner_tokens tables, minting and verifying runner tokens
// (hmr_<prefix>_<secret>, scoped to one organization and one pool, stored as
// sha256 only), and the runner rows the hosted coordinators keep up to date
// (engine.RunnerBook). The runner protocol itself is the worker API of
// engine.Coordinator; local mode never uses this package.
package runners

import (
	"context"
	"crypto/rand"
	"database/sql"
	"embed"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/maximhq/bifrost/bench/auth"
	"github.com/maximhq/bifrost/bench/db"
	"github.com/maximhq/bifrost/bench/schemas"
)

//go:embed migrations/*.sql
var migrations embed.FS

func init() { db.Register("runners", migrations) }

// Defaults.
const (
	// DefaultStaleAfter marks a runner silent this long as lost; the
	// coordinator's lease stale threshold (engine.DefaultStaleAfter).
	DefaultStaleAfter = 2 * time.Minute
	// DefaultTouchEvery is how often a runner's lease and heartbeat calls
	// rewrite its row when nothing else changed.
	DefaultTouchEvery = 15 * time.Second
	// MaxTokenDays caps a token's lifetime when one is asked for.
	MaxTokenDays = 3650

	tokenLookupLen = 8
	secretBytes    = 32
	lookupAlphabet = "abcdefghijklmnopqrstuvwxyz0123456789"
)

// Errors. ErrInvalid wraps schemas.ErrWorkerRequest so the worker routes
// answer 400; ErrNotFound wraps schemas.ErrNotFound.
var (
	ErrInvalid  = fmt.Errorf("invalid runner request: %w", schemas.ErrWorkerRequest)
	ErrNotFound = fmt.Errorf("runner: %w", schemas.ErrNotFound)
	// ErrBadToken is an unknown, malformed, revoked or expired runner
	// token. Callers answer 401 and never say which.
	ErrBadToken = errors.New("runner token unknown, revoked or expired")
)

// Live is the coordinators' live view of an organization's runners
// (engine.RemoteHub).
type Live interface {
	Workers(orgID string) []schemas.WorkerInfo
	Forget(orgID, workerID, why string) bool
	ForgetToken(orgID, tokenID, why string) int
}

// Options configures a Service.
type Options struct {
	StaleAfter time.Duration
	TouchEvery time.Duration
	// PublicURL is the hosted server's address, written into the runner
	// start and install commands a new token is shown with.
	PublicURL string
	// Repo and Ref, when set, go into the install command as --repo and
	// --ref: the git repository and commit the server was built from, which
	// a runner must match.
	Repo   string
	Ref    string
	Live   Live
	Logger schemas.Logger
	Now    func() time.Time
}

// Service is the runners area over the database.
type Service struct {
	db   *db.DB
	opts Options

	mu      sync.Mutex
	touched map[string]touch // org/name -> last row write
	used    map[string]int64 // token id -> last last_used_at write
}

type touch struct {
	at      time.Time
	state   string
	running int
}

// New returns the service. The runners migration set is registered by this
// package; d must be migrated.
func New(d *db.DB, opts Options) *Service {
	if opts.StaleAfter <= 0 {
		opts.StaleAfter = DefaultStaleAfter
	}
	if opts.TouchEvery <= 0 {
		opts.TouchEvery = DefaultTouchEvery
	}
	if opts.Logger == nil {
		opts.Logger = schemas.NoopLogger{}
	}
	if opts.Now == nil {
		opts.Now = time.Now
	}
	opts.PublicURL = strings.TrimRight(opts.PublicURL, "/")
	return &Service{db: d, opts: opts, touched: map[string]touch{}, used: map[string]int64{}}
}

// SetLive attaches the coordinators' live view once it exists (the hub is
// built after the service when serve wires them).
func (s *Service) SetLive(l Live) {
	s.mu.Lock()
	s.opts.Live = l
	s.mu.Unlock()
}

func (s *Service) live() Live {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.opts.Live
}

func (s *Service) now() time.Time { return s.opts.Now().UTC() }

// NewToken returns a fresh runner token hmr_<8 [a-z0-9]>_<43 base64url> and
// its lookup prefix.
func NewToken() (token, prefix string, err error) {
	out := make([]byte, 0, tokenLookupLen)
	buf := make([]byte, 1)
	for len(out) < tokenLookupLen {
		if _, err := rand.Read(buf); err != nil {
			return "", "", fmt.Errorf("read random: %w", err)
		}
		if buf[0] < 252 { // 252 = 7*36: unbiased
			out = append(out, lookupAlphabet[int(buf[0])%len(lookupAlphabet)])
		}
	}
	secret := make([]byte, secretBytes)
	if _, err := rand.Read(secret); err != nil {
		return "", "", fmt.Errorf("read random: %w", err)
	}
	prefix = string(out)
	return auth.RunnerTokenPrefix + prefix + "_" + base64.RawURLEncoding.EncodeToString(secret), prefix, nil
}

// ParseToken checks the shape of a runner token and returns its prefix.
func ParseToken(token string) (prefix string, ok bool) {
	rest, ok := strings.CutPrefix(token, auth.RunnerTokenPrefix)
	if !ok || len(rest) != tokenLookupLen+1+43 || rest[tokenLookupLen] != '_' {
		return "", false
	}
	prefix = rest[:tokenLookupLen]
	for _, c := range prefix {
		if !strings.ContainsRune(lookupAlphabet, c) {
			return "", false
		}
	}
	if _, err := base64.RawURLEncoding.DecodeString(rest[tokenLookupLen+1:]); err != nil {
		return "", false
	}
	return prefix, true
}

// CreateToken mints a runner token for one pool of the organization. The
// answer carries the token, the only time it is shown, and the exact
// commands that start a runner with it.
func (s *Service) CreateToken(ctx context.Context, orgID, createdBy string, req schemas.RunnerTokenCreate) (*schemas.RunnerTokenCreated, error) {
	pool := strings.TrimSpace(req.Pool)
	if pool == "" {
		pool = schemas.DefaultRunnerPool
	}
	if orgID == "" {
		return nil, fmt.Errorf("%w: no organization", ErrInvalid)
	}
	if !schemas.ValidPoolName(pool) {
		return nil, fmt.Errorf("%w: pool %q must be 1 to 64 letters, digits, '.', '_' or '-'", ErrInvalid, pool)
	}
	name := strings.TrimSpace(req.Name)
	if len(name) > 128 || strings.ContainsAny(name, "\r\n\t") {
		return nil, fmt.Errorf("%w: name must be at most 128 characters on one line", ErrInvalid)
	}
	if req.ExpiresInDays < 0 || req.ExpiresInDays > MaxTokenDays {
		return nil, fmt.Errorf("%w: expires_in_days must be 0 (never) to %d", ErrInvalid, MaxTokenDays)
	}
	token, prefix, err := NewToken()
	if err != nil {
		return nil, err
	}
	now := s.now()
	var expires sql.NullInt64
	if req.ExpiresInDays > 0 {
		expires = db.NullMillis(now.Add(time.Duration(req.ExpiresInDays) * 24 * time.Hour))
	}
	id := db.NewID("rtk")
	_, err = s.db.Exec(ctx, `INSERT INTO runner_tokens (id, org_id, pool, name, prefix, hash, created_by, created_at, expires_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`, id, orgID, pool, name, prefix, auth.HashSecret(token), createdBy, db.Millis(now), expires)
	if err != nil {
		if db.IsForeignKey(err) {
			return nil, fmt.Errorf("organization %s: %w", orgID, ErrNotFound)
		}
		return nil, fmt.Errorf("create runner token: %w", err)
	}
	view, err := s.token(ctx, orgID, id)
	if err != nil {
		return nil, err
	}
	return &schemas.RunnerTokenCreated{Token: token, Info: *view, Command: s.StartCommand(pool, name), Install: s.InstallCommand(pool, name)}, nil
}

// StartCommand is the `bifrost-bench runner start` line for a token of pool,
// run from the bench folder of the runner's checkout with the token saved in
// ~/.heimdall-runner-token (mode 0600).
func (s *Service) StartCommand(pool, name string) string {
	cmd := "bifrost-bench runner start -server " + shellQuote(s.serverURL()) + " -token-file ~/.heimdall-runner-token -pool " + shellQuote(pool)
	if n := runnerName(name); n != "" {
		cmd += " -name " + shellQuote(n)
	}
	return cmd
}

// InstallCommand is the install script line for a fresh Ubuntu or Debian box
// (deploy/runner/install.sh), reading the token from ./runner-token.
func (s *Service) InstallCommand(pool, name string) string {
	cmd := "curl -fsSLo install.sh " + shellQuote(s.serverURL()+"/api/bench/runners/install.sh") +
		" && sudo bash install.sh --server " + shellQuote(s.serverURL()) + " --token-file ./runner-token --pool " + shellQuote(pool)
	if n := runnerName(name); n != "" {
		cmd += " --name " + shellQuote(n)
	}
	if s.opts.Repo != "" {
		cmd += " --repo " + shellQuote(s.opts.Repo)
	}
	if s.opts.Ref != "" {
		cmd += " --ref " + shellQuote(s.opts.Ref)
	}
	return cmd
}

func (s *Service) serverURL() string {
	if s.opts.PublicURL == "" {
		return "https://heimdall.example.com"
	}
	return s.opts.PublicURL
}

// runnerName turns a token's label into a runner name when it is one.
func runnerName(name string) string {
	if schemas.ValidPoolName(name) {
		return name
	}
	return ""
}

// shellQuote quotes s for a POSIX shell when it needs it.
func shellQuote(s string) string {
	safe := s != ""
	for _, r := range s {
		if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || strings.ContainsRune("-_.:/@=+~,", r)) {
			safe = false
			break
		}
	}
	if safe {
		return s
	}
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

const tokenCols = `id, org_id, pool, name, prefix, created_by, created_at, expires_at, last_used_at, revoked_at`

func (s *Service) scanToken(r interface{ Scan(...any) error }) (*schemas.RunnerTokenView, error) {
	var v schemas.RunnerTokenView
	var created int64
	var expires, used, revoked sql.NullInt64
	if err := r.Scan(&v.ID, &v.OrgID, &v.Pool, &v.Name, &v.Prefix, &v.CreatedBy, &created, &expires, &used, &revoked); err != nil {
		return nil, err
	}
	v.CreatedAt = db.Time(created)
	v.ExpiresAt, v.LastUsedAt, v.RevokedAt = optTime(expires), optTime(used), optTime(revoked)
	switch {
	case v.RevokedAt != nil:
		v.State = schemas.RunnerTokenRevoked
	case v.ExpiresAt != nil && !s.now().Before(*v.ExpiresAt):
		v.State = schemas.RunnerTokenExpired
	default:
		v.State = schemas.RunnerTokenActive
	}
	return &v, nil
}

func optTime(v sql.NullInt64) *time.Time {
	if !v.Valid {
		return nil
	}
	t := db.Time(v.Int64)
	return &t
}

func (s *Service) token(ctx context.Context, orgID, id string) (*schemas.RunnerTokenView, error) {
	v, err := s.scanToken(s.db.QueryRow(ctx, `SELECT `+tokenCols+` FROM runner_tokens WHERE org_id = ? AND id = ?`, orgID, id))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("runner token %s: %w", id, ErrNotFound)
	}
	return v, err
}

// ListTokens lists the organization's runner tokens, newest first, without
// their secrets.
func (s *Service) ListTokens(ctx context.Context, orgID string) ([]schemas.RunnerTokenView, error) {
	rows, err := s.db.Query(ctx, `SELECT `+tokenCols+` FROM runner_tokens WHERE org_id = ? ORDER BY created_at DESC, id DESC`, orgID)
	if err != nil {
		return nil, fmt.Errorf("list runner tokens: %w", err)
	}
	defer rows.Close()
	out := []schemas.RunnerTokenView{}
	for rows.Next() {
		v, err := s.scanToken(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *v)
	}
	return out, rows.Err()
}

// RevokeToken revokes a token at once: the next call of any runner holding
// it is refused, and the runners registered with it lose their leases now.
// Revoking a revoked token changes nothing.
func (s *Service) RevokeToken(ctx context.Context, orgID, id string) (*schemas.RunnerTokenView, error) {
	if _, err := s.token(ctx, orgID, id); err != nil {
		return nil, err
	}
	if _, err := s.db.Exec(ctx, `UPDATE runner_tokens SET revoked_at = ? WHERE org_id = ? AND id = ? AND revoked_at IS NULL`,
		db.Millis(s.now()), orgID, id); err != nil {
		return nil, fmt.Errorf("revoke runner token: %w", err)
	}
	if l := s.live(); l != nil {
		if n := l.ForgetToken(orgID, id, "its runner token was revoked"); n > 0 {
			s.opts.Logger.Warn("runner token revoked; its runners were removed", "org", orgID, "token", id, "runners", n)
		}
	}
	return s.token(ctx, orgID, id)
}

// Verify resolves a runner token to its scope. A bad, revoked or expired
// token is ErrBadToken; a database failure is returned as such.
func (s *Service) Verify(ctx context.Context, token string) (schemas.RunnerScope, error) {
	if _, ok := ParseToken(token); !ok {
		return schemas.RunnerScope{}, ErrBadToken
	}
	var id, org, pool string
	var expires, revoked sql.NullInt64
	err := s.db.QueryRow(ctx, `SELECT id, org_id, pool, expires_at, revoked_at FROM runner_tokens WHERE hash = ?`,
		auth.HashSecret(token)).Scan(&id, &org, &pool, &expires, &revoked)
	if errors.Is(err, sql.ErrNoRows) {
		return schemas.RunnerScope{}, ErrBadToken
	}
	if err != nil {
		return schemas.RunnerScope{}, fmt.Errorf("verify runner token: %w", err)
	}
	now := s.now()
	if revoked.Valid || (expires.Valid && now.UnixMilli() >= expires.Int64) {
		return schemas.RunnerScope{}, ErrBadToken
	}
	s.markUsed(ctx, id, now)
	return schemas.RunnerScope{OrgID: org, Pool: pool, TokenID: id}, nil
}

// VerifyPrincipal is Verify as the auth middleware's runner principal.
func (s *Service) VerifyPrincipal(ctx context.Context, token string) (*auth.Principal, error) {
	sc, err := s.Verify(ctx, token)
	if errors.Is(err, ErrBadToken) {
		return nil, auth.ErrInvalidToken
	}
	if err != nil {
		return nil, err // a backend failure: auth answers 503, the runner retries
	}
	return &auth.Principal{Kind: auth.KindRunner, OrgID: sc.OrgID, TokenID: sc.TokenID, Pool: sc.Pool}, nil
}

// markUsed writes last_used_at at most once per TouchEvery per token.
func (s *Service) markUsed(ctx context.Context, id string, now time.Time) {
	s.mu.Lock()
	last := s.used[id]
	if now.UnixMilli()-last < s.opts.TouchEvery.Milliseconds() {
		s.mu.Unlock()
		return
	}
	s.used[id] = now.UnixMilli()
	s.mu.Unlock()
	if _, err := s.db.Exec(ctx, `UPDATE runner_tokens SET last_used_at = ? WHERE id = ?`, now.UnixMilli(), id); err != nil {
		s.opts.Logger.Warn("record runner token use", "token", id, "error", err.Error())
	}
}

// RunnerSeen records a hosted runner's registration or activity in the
// runners table (engine.RunnerBook). A registration always writes; lease
// and heartbeat calls write when the state or running count changed, or
// TouchEvery passed. Failures are logged, never returned: the protocol does
// not depend on the table.
func (s *Service) RunnerSeen(ctx context.Context, scope schemas.RunnerScope, info schemas.WorkerInfo, registered bool) {
	key := scope.OrgID + "/" + info.ID
	now := s.now()
	t := touch{at: now, state: info.State, running: len(info.Running)}
	s.mu.Lock()
	last, ok := s.touched[key]
	if !registered && ok && last.state == t.state && last.running == t.running && now.Sub(last.at) < s.opts.TouchEvery {
		s.mu.Unlock()
		return
	}
	s.touched[key] = t
	s.mu.Unlock()
	if err := s.upsert(ctx, scope, info, now, registered); err != nil {
		s.opts.Logger.Warn("record runner", "org", scope.OrgID, "runner", info.ID, "error", err.Error())
		s.mu.Lock()
		delete(s.touched, key)
		s.mu.Unlock()
	}
}

func (s *Service) upsert(ctx context.Context, scope schemas.RunnerScope, info schemas.WorkerInfo, now time.Time, registered bool) error {
	labels := info.Labels
	if labels == nil {
		labels = []string{}
	}
	lb, err := json.Marshal(labels)
	if err != nil {
		return err
	}
	state := info.State
	if state == schemas.WorkerLost || state == "" {
		state = schemas.WorkerIdle // it called just now
	}
	pool := info.Pool
	if pool == "" {
		pool = scope.Pool
	}
	ms := now.UnixMilli()
	q := `INSERT INTO runners (id, org_id, name, pool, labels, version, host, slots, running, token_id, status, last_seen, registered_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT (org_id, name) DO UPDATE SET pool = excluded.pool, labels = excluded.labels, version = excluded.version,
			host = excluded.host, slots = excluded.slots, running = excluded.running, token_id = excluded.token_id,
			status = excluded.status, last_seen = excluded.last_seen`
	if registered {
		q += `, registered_at = excluded.registered_at`
	}
	// A row another token registered keeps its token and pool: a runner
	// of a different token under the same name never takes it over, also
	// after a restart emptied the coordinator's memory (4-B-01).
	q += ` WHERE runners.token_id = excluded.token_id OR runners.token_id IS NULL OR runners.token_id = ''`
	_, err = s.db.Exec(ctx, q, db.NewID("rnr"), scope.OrgID, info.ID, pool, string(lb), info.Version, info.Host, info.Slots,
		len(info.Running), scope.TokenID, state, ms, ms)
	return err
}

const runnerCols = `r.id, r.org_id, r.name, r.pool, r.labels, r.version, r.host, r.slots, r.running, r.token_id,
	COALESCE(t.prefix, ''), r.status, r.last_seen, r.registered_at`

func (s *Service) scanRunner(r interface{ Scan(...any) error }) (*schemas.RunnerView, error) {
	var v schemas.RunnerView
	var labels string
	var seen, registered int64
	if err := r.Scan(&v.ID, &v.OrgID, &v.Name, &v.Pool, &labels, &v.Version, &v.Host, &v.Slots, &v.RunningSlots, &v.TokenID,
		&v.TokenPrefix, &v.State, &seen, &registered); err != nil {
		return nil, err
	}
	if err := json.Unmarshal([]byte(labels), &v.Labels); err != nil || v.Labels == nil {
		v.Labels = []string{}
	}
	v.LastSeen, v.RegisteredAt = db.Time(seen), db.Time(registered)
	v.Running = []string{}
	return &v, nil
}

// merge applies the live state: a runner the coordinator holds is shown as
// it sees it; any other runner silent past the stale threshold is lost.
func (s *Service) merge(v *schemas.RunnerView, live map[string]schemas.WorkerInfo) {
	if w, ok := live[v.Name]; ok && w.TokenID == v.TokenID {
		v.Live = true
		v.State = w.State
		v.Running = append([]string{}, w.Running...)
		v.RunningSlots = len(w.Running)
		v.Completed = w.Completed
		if w.LastSeen.After(v.LastSeen) {
			v.LastSeen = w.LastSeen
		}
		return
	}
	if s.now().Sub(v.LastSeen) > s.opts.StaleAfter {
		v.State = schemas.RunnerLost
		v.RunningSlots = 0
	}
}

func (s *Service) liveMap(orgID string) map[string]schemas.WorkerInfo {
	out := map[string]schemas.WorkerInfo{}
	if l := s.live(); l != nil {
		for _, w := range l.Workers(orgID) {
			out[w.ID] = w
		}
	}
	return out
}

// ListRunners lists the organization's runners by name, with their live
// state.
func (s *Service) ListRunners(ctx context.Context, orgID string) ([]schemas.RunnerView, error) {
	rows, err := s.db.Query(ctx, `SELECT `+runnerCols+` FROM runners r LEFT JOIN runner_tokens t ON t.id = r.token_id
		WHERE r.org_id = ? ORDER BY r.name`, orgID)
	if err != nil {
		return nil, fmt.Errorf("list runners: %w", err)
	}
	defer rows.Close()
	live := s.liveMap(orgID)
	out := []schemas.RunnerView{}
	for rows.Next() {
		v, err := s.scanRunner(rows)
		if err != nil {
			return nil, err
		}
		s.merge(v, live)
		out = append(out, *v)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

// GetRunner returns one of the organization's runners.
func (s *Service) GetRunner(ctx context.Context, orgID, id string) (*schemas.RunnerView, error) {
	v, err := s.scanRunner(s.db.QueryRow(ctx, `SELECT `+runnerCols+` FROM runners r LEFT JOIN runner_tokens t ON t.id = r.token_id
		WHERE r.org_id = ? AND r.id = ?`, orgID, id))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("runner %s: %w", id, ErrNotFound)
	}
	if err != nil {
		return nil, err
	}
	s.merge(v, s.liveMap(orgID))
	return v, nil
}

// RemoveRunner deletes a runner's row and drops its registration: its
// leases are lost at once and its slots go back to the queue. A runner whose
// token is still valid registers again on its next call; revoke the token to
// keep it out.
func (s *Service) RemoveRunner(ctx context.Context, orgID, id string) (*schemas.RunnerView, error) {
	v, err := s.GetRunner(ctx, orgID, id)
	if err != nil {
		return nil, err
	}
	if _, err := s.db.Exec(ctx, `DELETE FROM runners WHERE org_id = ? AND id = ?`, orgID, id); err != nil {
		return nil, fmt.Errorf("remove runner: %w", err)
	}
	s.mu.Lock()
	delete(s.touched, orgID+"/"+v.Name)
	s.mu.Unlock()
	if l := s.live(); l != nil {
		l.Forget(orgID, v.Name, "an admin removed the runner")
	}
	return v, nil
}
