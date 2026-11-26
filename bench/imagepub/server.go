package imagepub

import (
	"bytes"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// DefaultMaxBytes is the largest image tar the publisher accepts.
const DefaultMaxBytes int64 = 16 << 30

// reuseWindow is how long a pushed digest answers a repeat publish of the
// same ref with the same tar size without pushing again.
const reuseWindow = 10 * time.Minute

// Server is the image publisher: the only holder of the registry's write
// identity. It has no Docker socket; the shared runner streams it a
// `docker save` tar of an image it qualified, and the server pushes it with
// crane, then records the digest in the manifest.
type Server struct {
	Writer   string // registry-writer:5000
	AuthFile string // user:password, the sync identity
	Manifest string
	Token    string
	Crane    string // path of the crane binary
	Set      func() (map[string]string, error)
	MaxBytes int64 // default DefaultMaxBytes
	TempDir  string

	locks sync.Map // ref -> *sync.Mutex
	mu    sync.Mutex
	done  map[string]pushed
}

// pushed is a recent push of one ref.
type pushed struct {
	size   int64
	digest string
	at     time.Time
}

// Handler serves POST /v1/publish?ref=<ref>.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /v1/publish", s.publish)
	return mux
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

func errJSON(w http.ResponseWriter, code int, msg string) {
	writeJSON(w, code, map[string]string{"error": msg})
}

func (s *Server) authorized(r *http.Request) bool {
	got, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
	if !ok || s.Token == "" || got == "" {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(got), []byte(s.Token)) == 1
}

func (s *Server) publish(w http.ResponseWriter, r *http.Request) {
	if !s.authorized(r) {
		errJSON(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	ref := r.URL.Query().Get("ref")
	if !ValidRef(ref) {
		errJSON(w, http.StatusForbidden, ErrNotPlatform.Error())
		return
	}
	set, err := s.Set()
	if err != nil {
		slog.Error("image publisher: platform images", "err", err)
		errJSON(w, http.StatusInternalServerError, "platform image list unavailable")
		return
	}
	if _, ok := set[ref]; !ok {
		errJSON(w, http.StatusForbidden, ErrNotPlatform.Error())
		return
	}

	l, _ := s.locks.LoadOrStore(ref, &sync.Mutex{})
	mu := l.(*sync.Mutex)
	mu.Lock()
	defer mu.Unlock()

	max := s.MaxBytes
	if max <= 0 {
		max = DefaultMaxBytes
	}
	tmp, err := os.CreateTemp(s.tempDir(), "publish-*.tar")
	if err != nil {
		slog.Error("image publisher: temp file", "err", err)
		errJSON(w, http.StatusInternalServerError, "no room for the image")
		return
	}
	defer os.Remove(tmp.Name())
	size, err := io.Copy(tmp, http.MaxBytesReader(w, r.Body, max))
	if cerr := tmp.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		var tooBig *http.MaxBytesError
		if errors.As(err, &tooBig) {
			errJSON(w, http.StatusRequestEntityTooLarge, "image too large")
			return
		}
		errJSON(w, http.StatusBadRequest, "image upload failed")
		return
	}

	if d := s.recent(ref, size); d != "" {
		writeJSON(w, http.StatusOK, map[string]string{"ref": ref, "digest": d})
		return
	}
	digest, err := s.push(r, ref, tmp.Name())
	if err != nil {
		slog.Error("image publisher: push", "ref", ref, "err", err)
		errJSON(w, http.StatusBadGateway, "the registry did not take the image")
		return
	}
	if err := SetPin(s.Manifest, ref, digest); err != nil {
		slog.Error("image publisher: manifest", "ref", ref, "err", err)
		errJSON(w, http.StatusInternalServerError, "the manifest could not be written")
		return
	}
	s.mu.Lock()
	if s.done == nil {
		s.done = map[string]pushed{}
	}
	s.done[ref] = pushed{size: size, digest: digest, at: time.Now()}
	s.mu.Unlock()
	slog.Info("image publisher: published", "ref", ref, "digest", digest)
	writeJSON(w, http.StatusOK, map[string]string{"ref": ref, "digest": digest})
}

func (s *Server) tempDir() string {
	if s.TempDir != "" {
		return s.TempDir
	}
	return os.TempDir()
}

// recent returns the digest of a push of ref with the same tar size in the
// last reuseWindow, or "".
func (s *Server) recent(ref string, size int64) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	p, ok := s.done[ref]
	if !ok || p.size != size || time.Since(p.at) > reuseWindow {
		return ""
	}
	return p.digest
}

// push logs crane in with the sync identity (the password on stdin, never on
// a command line) and pushes the tar, returning the pushed digest. No shell
// runs: crane is executed directly.
func (s *Server) push(r *http.Request, ref, tar string) (string, error) {
	raw, err := os.ReadFile(s.AuthFile)
	if err != nil {
		return "", fmt.Errorf("auth file: %w", err)
	}
	user, pass, ok := strings.Cut(strings.TrimSpace(string(raw)), ":")
	if !ok || user == "" {
		return "", errors.New("auth file: want user:password")
	}
	ctx := r.Context()
	// crane keeps its login in DOCKER_CONFIG; the container's root file
	// system is read-only, so it lives under the system temp folder (a tmpfs
	// in the hosted compose), never in the upload volume at rest.
	env := append(os.Environ(), "DOCKER_CONFIG="+filepath.Join(os.TempDir(), "heimdall-crane"))
	login := exec.CommandContext(ctx, s.Crane, "auth", "login", s.Writer, "-u", user, "--password-stdin")
	login.Stdin = strings.NewReader(pass)
	login.Env = env
	if out, err := login.CombinedOutput(); err != nil {
		return "", fmt.Errorf("crane auth login: %w: %s", err, bytes.TrimSpace(out))
	}
	var stdout, stderr bytes.Buffer
	cmd := exec.CommandContext(ctx, s.Crane, "push", "--insecure", tar, s.Writer+"/"+ref)
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	cmd.Env = env
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("crane push: %w: %s", err, bytes.TrimSpace(stderr.Bytes()))
	}
	out := strings.TrimSpace(stdout.String())
	i := strings.LastIndex(out, "@")
	if i < 0 {
		return "", fmt.Errorf("crane push: no digest in %q", out)
	}
	digest := strings.TrimSpace(out[i+1:])
	if !digestRe.MatchString(digest) {
		return "", fmt.Errorf("crane push: bad digest %q", digest)
	}
	return digest, nil
}
