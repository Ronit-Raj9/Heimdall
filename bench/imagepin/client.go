package imagepin

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

// Client reads manifests from a registry (Distribution API v2) with basic
// or token authentication: on a 401 with a Bearer challenge it fetches a
// token from the challenge's realm with User and Password, as the docker
// CLI does. It never writes; pushes go through docker (sync.go).
type Client struct {
	Host           string // host[:port]
	PlainHTTP      bool   // http:// instead of https:// (a local registry)
	User, Password string
	HTTP           *http.Client

	mu     sync.Mutex
	tokens map[string]string // scope -> bearer token
}

// manifestAccept lists every manifest and index media type.
var manifestAccept = strings.Join([]string{
	"application/vnd.oci.image.index.v1+json", "application/vnd.oci.image.manifest.v1+json",
	"application/vnd.docker.distribution.manifest.list.v2+json", "application/vnd.docker.distribution.manifest.v2+json",
}, ", ")

func (c *Client) base() string {
	if c.PlainHTTP {
		return "http://" + c.Host
	}
	return "https://" + c.Host
}

func (c *Client) http() *http.Client {
	if c.HTTP != nil {
		return c.HTTP
	}
	return &http.Client{Timeout: 60 * time.Second}
}

// ErrNotFound is a manifest the registry does not have.
var ErrNotFound = errors.New("not in the registry")

// Digest returns the digest of repo's manifest at reference (a tag or a
// digest), from the registry's Docker-Content-Digest header.
func (c *Client) Digest(ctx context.Context, repo, reference string) (string, error) {
	resp, err := c.do(ctx, http.MethodHead, repo, "/v2/"+repo+"/manifests/"+reference)
	if err != nil {
		return "", err
	}
	resp.Body.Close()
	d := resp.Header.Get("Docker-Content-Digest")
	if !digestRe.MatchString(d) {
		return "", fmt.Errorf("registry %s: no digest for %s:%s", c.Host, repo, reference)
	}
	return d, nil
}

// Manifest returns repo's manifest at reference: its media type and body.
func (c *Client) Manifest(ctx context.Context, repo, reference string) (string, []byte, error) {
	resp, err := c.do(ctx, http.MethodGet, repo, "/v2/"+repo+"/manifests/"+reference)
	if err != nil {
		return "", nil, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return "", nil, err
	}
	return resp.Header.Get("Content-Type"), body, nil
}

func (c *Client) do(ctx context.Context, method, repo, path string) (*http.Response, error) {
	scope := "repository:" + repo + ":pull"
	for attempt := 0; attempt < 2; attempt++ {
		req, err := http.NewRequestWithContext(ctx, method, c.base()+path, nil)
		if err != nil {
			return nil, err
		}
		req.Header.Set("Accept", manifestAccept)
		c.mu.Lock()
		tok := c.tokens[scope]
		c.mu.Unlock()
		switch {
		case tok != "":
			req.Header.Set("Authorization", "Bearer "+tok)
		case c.User != "" && attempt > 0:
			req.SetBasicAuth(c.User, c.Password)
		}
		resp, err := c.http().Do(req)
		if err != nil {
			return nil, fmt.Errorf("registry %s: %w", c.Host, err)
		}
		switch resp.StatusCode {
		case http.StatusOK:
			return resp, nil
		case http.StatusNotFound:
			resp.Body.Close()
			return nil, fmt.Errorf("%s %s: %w", c.Host, strings.TrimPrefix(path, "/v2/"), ErrNotFound)
		case http.StatusUnauthorized:
			ch := resp.Header.Get("WWW-Authenticate")
			resp.Body.Close()
			if attempt > 0 {
				return nil, fmt.Errorf("registry %s: 401 for %s: the credentials were refused", c.Host, repo)
			}
			if scheme, params := parseChallenge(ch); scheme == "bearer" {
				t, err := c.fetchToken(ctx, params, scope)
				if err != nil {
					return nil, err
				}
				c.mu.Lock()
				if c.tokens == nil {
					c.tokens = map[string]string{}
				}
				c.tokens[scope] = t
				c.mu.Unlock()
			}
			continue
		default:
			resp.Body.Close()
			return nil, fmt.Errorf("registry %s: %s for %s", c.Host, resp.Status, strings.TrimPrefix(path, "/v2/"))
		}
	}
	return nil, fmt.Errorf("registry %s: authentication failed for %s", c.Host, repo)
}

// fetchToken asks the challenge's realm for a token for scope.
func (c *Client) fetchToken(ctx context.Context, params map[string]string, scope string) (string, error) {
	realm := params["realm"]
	u, err := url.Parse(realm)
	if err != nil || (u.Scheme != "https" && u.Scheme != "http") {
		return "", fmt.Errorf("registry %s: bad token realm %q", c.Host, realm)
	}
	q := u.Query()
	if s := params["service"]; s != "" {
		q.Set("service", s)
	}
	q.Set("scope", scope)
	u.RawQuery = q.Encode()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return "", err
	}
	if c.User != "" {
		req.SetBasicAuth(c.User, c.Password)
	}
	resp, err := c.http().Do(req)
	if err != nil {
		return "", fmt.Errorf("token server %s: %w", u.Host, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("token server %s: %s", u.Host, resp.Status)
	}
	var out struct {
		Token       string `json:"token"`
		AccessToken string `json:"access_token"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 64<<10)).Decode(&out); err != nil {
		return "", fmt.Errorf("token server %s: %w", u.Host, err)
	}
	if out.Token == "" {
		out.Token = out.AccessToken
	}
	if out.Token == "" {
		return "", fmt.Errorf("token server %s: no token in the answer", u.Host)
	}
	return out.Token, nil
}

// parseChallenge splits `Bearer realm="x",service="y"` into the lower-case
// scheme and its parameters.
func parseChallenge(h string) (string, map[string]string) {
	scheme, rest, _ := strings.Cut(strings.TrimSpace(h), " ")
	params := map[string]string{}
	for rest != "" {
		rest = strings.TrimLeft(rest, " ,")
		k, v, ok := strings.Cut(rest, "=")
		if !ok {
			break
		}
		k = strings.ToLower(strings.TrimSpace(k))
		if strings.HasPrefix(v, `"`) {
			end := strings.Index(v[1:], `"`)
			if end < 0 {
				break
			}
			params[k], rest = v[1:end+1], v[end+2:]
			continue
		}
		val, tail, _ := strings.Cut(v, ",")
		params[k], rest = strings.TrimSpace(val), tail
	}
	return strings.ToLower(scheme), params
}
