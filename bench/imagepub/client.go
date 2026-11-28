package imagepub

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/maximhq/bifrost/bench/utils"
)

// DefaultTimeout bounds one publish (save, upload, push).
const DefaultTimeout = 20 * time.Minute

// Client is the shared runner's side of the publisher: it qualifies an
// image on its own host and streams it to the publisher service.
type Client struct {
	URL       string
	TokenFile string
	Docker    utils.Docker // no policy
	Manifest  string
	Set       func() (map[string]string, error)
	Platform  []string
	Timeout   time.Duration // default DefaultTimeout
	HTTP      *http.Client  // nil: http.DefaultClient

	mu    sync.Mutex
	calls map[string]*call
}

// call is one publish in flight, shared by every caller for its ref.
type call struct {
	done    chan struct{}
	waiters int
	digest  string
	err     error
}

// inflight is the number of callers sharing the publish of ref (tests).
func (c *Client) inflight(ref string) int {
	c.mu.Lock()
	defer c.mu.Unlock()
	if cl, ok := c.calls[ref]; ok {
		return cl.waiters
	}
	return 0
}

// Ensure returns the registry digest of the platform image ref, publishing
// it first when the manifest has none. Concurrent calls for one ref share
// one publish. It matches utils.PublishFunc.
func (c *Client) Ensure(ctx context.Context, ref string) (string, error) {
	if pins, err := utils.ReadRegistryManifest(c.Manifest); err == nil {
		if d, ok := pins[ref]; ok {
			return d, nil
		}
	}
	c.mu.Lock()
	if c.calls == nil {
		c.calls = map[string]*call{}
	}
	if cl, ok := c.calls[ref]; ok {
		cl.waiters++
		c.mu.Unlock()
		select {
		case <-cl.done:
			return cl.digest, cl.err
		case <-ctx.Done():
			return "", ctx.Err()
		}
	}
	cl := &call{done: make(chan struct{}), waiters: 1}
	c.calls[ref] = cl
	c.mu.Unlock()

	cl.digest, cl.err = c.publish(ctx, ref)
	c.mu.Lock()
	delete(c.calls, ref)
	c.mu.Unlock()
	close(cl.done)
	return cl.digest, cl.err
}

func (c *Client) publish(ctx context.Context, ref string) (string, error) {
	timeout := c.Timeout
	if timeout <= 0 {
		timeout = DefaultTimeout
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	set, err := c.Set()
	if err != nil {
		return "", fmt.Errorf("platform images: %w", err)
	}
	if err := Qualify(ctx, c.Docker, ref, set, c.Platform); err != nil {
		return "", err
	}
	return c.send(ctx, ref, ref)
}

// PublishOwn copies an organization's own host image hostRef
// (org-<id>/<name>:<tag>) to the registry as pushRef, the same repository
// with the host image ID as its tag, and returns the pushed digest. It
// matches utils.PublishOwnFunc. Nothing is written to the manifest.
func (c *Client) PublishOwn(ctx context.Context, hostRef, pushRef string) (string, error) {
	if !ValidOwnRef(pushRef) {
		return "", fmt.Errorf("%s: not an organization image name", pushRef)
	}
	repo := pushRef[:strings.LastIndex(pushRef, ":")]
	if i := strings.LastIndex(hostRef, ":"); i <= strings.LastIndex(hostRef, "/") || hostRef[:i] != repo {
		return "", fmt.Errorf("%s: not the host image of %s", hostRef, pushRef)
	}
	timeout := c.Timeout
	if timeout <= 0 {
		timeout = DefaultTimeout
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	return c.send(ctx, hostRef, pushRef)
}

// send streams a `docker save` of saveRef to the publisher, which pushes it
// as ref, and returns the pushed digest.
func (c *Client) send(ctx context.Context, saveRef, ref string) (string, error) {
	raw, err := os.ReadFile(c.TokenFile)
	if err != nil {
		return "", fmt.Errorf("image publisher token: %w", err)
	}
	token := strings.TrimSpace(string(raw))

	pr, pw := io.Pipe()
	save := c.Docker.Command(ctx, "save", saveRef)
	save.Stdout = pw
	var stderr strings.Builder
	save.Stderr = &stderr
	if err := save.Start(); err != nil {
		return "", fmt.Errorf("docker save %s: %w", saveRef, err)
	}
	go func() {
		err := save.Wait()
		if err != nil {
			err = fmt.Errorf("docker save %s: %w: %s", saveRef, err, strings.TrimSpace(stderr.String()))
		}
		pw.CloseWithError(err)
	}()

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(c.URL, "/")+"/v1/publish?ref="+url.QueryEscape(ref), pr)
	if err != nil {
		pr.CloseWithError(err)
		return "", err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/x-tar")
	hc := c.HTTP
	if hc == nil {
		hc = http.DefaultClient
	}
	resp, err := hc.Do(req)
	pr.CloseWithError(errors.New("request finished"))
	if err != nil {
		return "", fmt.Errorf("image publisher: %w", err)
	}
	defer resp.Body.Close()
	var ans struct {
		Ref    string `json:"ref"`
		Digest string `json:"digest"`
		Error  string `json:"error"`
	}
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<16))
	_ = json.Unmarshal(body, &ans)
	if resp.StatusCode != http.StatusOK {
		msg := ans.Error
		if msg == "" {
			msg = resp.Status
		}
		return "", fmt.Errorf("image publisher: %s", msg)
	}
	if !digestRe.MatchString(ans.Digest) {
		return "", fmt.Errorf("image publisher: bad digest %q", ans.Digest)
	}
	return ans.Digest, nil
}
