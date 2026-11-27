package sandbox

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"
)

// E2B (https://e2b.dev/docs/api-reference). The platform API creates
// templates and sandboxes with the team's API key (X-API-Key); envd, the
// daemon inside each sandbox, runs processes (Connect protocol, server
// streaming) and moves files with the sandbox's own access token.
//
//	POST   {api}/v3/templates                                   template for an image
//	POST   {api}/v2/templates/{tid}/builds/{bid}                build it fromImage
//	GET    {api}/templates/{tid}/builds/{bid}/status            building | ready | error
//	POST   {api}/v2/sandboxes                                   create (network rules, timeout)
//	DELETE {api}/sandboxes/{id}                                 kill
//	POST   {envd}/process.Process/Start                         run a command (Connect stream)
//	POST   {envd}/files?path=&username=root                     upload (raw body)
//	GET    {envd}/files?path=&username=root                     download
//
// envd is reached on the shared sandbox host with the E2b-Sandbox-Id and
// E2b-Sandbox-Port headers.
const (
	E2BDomain = "e2b.app"

	e2bEnvdPort       = "49983"
	e2bBuildTimeout   = 30 * time.Minute
	e2bOutputCap      = 1 << 20
	e2bMaxFrame       = 8 << 20
	e2bTemplatePrefix = "heimdall-"
)

// e2bPollEvery is how often a template build's status is read.
var e2bPollEvery = 3 * time.Second

func init() {
	Register(Info{
		Name: PlatformE2B, Title: "E2B",
		TokenHint:           "A team API key from the E2B dashboard (e2b_...).",
		EndpointLabel:       "Domain (only for a self-hosted E2B)",
		EndpointPlaceholder: "e2b.your-company.com",
		DocsURL:             "https://e2b.dev/docs/api-key",
	}, func(c Credentials) (Platform, error) { return NewE2B(c, nil), nil })
}

// E2B is the E2B client.
type E2B struct {
	api      *apiClient
	envdBase string
	http     *http.Client

	mu        sync.Mutex
	templates map[string]string // template name -> template id built in this process
}

// NewE2B returns an E2B client; hc nil uses the default client. The
// endpoint is the E2B domain (api.<domain>, sandbox.<domain>); a full URL
// sets the API URL and envd is then <url>/envd (tests).
func NewE2B(c Credentials, hc *http.Client) *E2B {
	domain := strings.TrimSpace(c.Endpoint)
	apiBase, envd := "https://api."+E2BDomain, "https://sandbox."+E2BDomain
	switch {
	case strings.HasPrefix(domain, "http://") || strings.HasPrefix(domain, "https://"):
		apiBase, envd = strings.TrimRight(domain, "/"), strings.TrimRight(domain, "/")+"/envd"
	case domain != "":
		apiBase, envd = "https://api."+domain, "https://sandbox."+domain
	}
	tok := strings.TrimSpace(c.Token)
	return &E2B{http: hc, envdBase: envd, templates: map[string]string{},
		api: &apiClient{http: hc, base: apiBase, title: "E2B", headers: map[string]string{"X-API-Key": tok}, secrets: []string{tok}}}
}

// Name implements Platform.
func (e *E2B) Name() string { return PlatformE2B }

// Check implements Platform: list the team's sandboxes.
func (e *E2B) Check(ctx context.Context) error {
	return e.api.json(ctx, "check token", http.MethodGet, "/sandboxes", nil, nil)
}

// templateName is the template an image (with its resources) is built as.
func templateName(image string, cpus, memMB int) string {
	sum := sha256.Sum256([]byte(fmt.Sprintf("%s|%d|%d", image, cpus, memMB)))
	return e2bTemplatePrefix + hex.EncodeToString(sum[:])[:20]
}

// ensureTemplate builds (once per process; E2B caches the layers) the
// template that runs image and returns its id.
func (e *E2B) ensureTemplate(ctx context.Context, image string, cpus, memMB int) (string, error) {
	name := templateName(image, cpus, memMB)
	e.mu.Lock()
	id, ok := e.templates[name]
	e.mu.Unlock()
	if ok {
		return id, nil
	}
	var tpl struct {
		TemplateID string `json:"templateID"`
		BuildID    string `json:"buildID"`
	}
	if err := e.api.json(ctx, "create template", http.MethodPost, "/v3/templates",
		map[string]any{"name": name, "cpuCount": cpus, "memoryMB": memMB}, &tpl); err != nil {
		return "", err
	}
	if tpl.TemplateID == "" || tpl.BuildID == "" {
		return "", errors.New("E2B create template: the answer has no template or build id")
	}
	bpath := "/v2/templates/" + url.PathEscape(tpl.TemplateID) + "/builds/" + url.PathEscape(tpl.BuildID)
	if err := e.api.json(ctx, "start template build", http.MethodPost, bpath, map[string]any{"fromImage": image, "steps": []any{}}, nil); err != nil {
		return "", err
	}
	bctx, cancel := context.WithTimeout(ctx, e2bBuildTimeout)
	defer cancel()
	spath := "/templates/" + url.PathEscape(tpl.TemplateID) + "/builds/" + url.PathEscape(tpl.BuildID) + "/status"
	err := poll(bctx, e2bPollEvery, func() (bool, error) {
		var st struct {
			Status string `json:"status"`
			Reason *struct {
				Message string `json:"message"`
				Step    string `json:"step"`
			} `json:"reason"`
		}
		if err := e.api.json(bctx, "template build status", http.MethodGet, spath, nil, &st); err != nil {
			return false, err
		}
		switch st.Status {
		case "ready":
			return true, nil
		case "error":
			msg := "no reason given"
			if st.Reason != nil && st.Reason.Message != "" {
				msg = st.Reason.Message
			}
			return false, fmt.Errorf("E2B template build for %s failed: %s", image, msg)
		}
		return false, nil
	})
	if err != nil {
		return "", err
	}
	e.mu.Lock()
	e.templates[name] = tpl.TemplateID
	e.mu.Unlock()
	return tpl.TemplateID, nil
}

// e2bNetwork is the create request's network rule for eg.
func e2bNetwork(eg Egress) (bool, map[string]any) {
	if eg.BlockAll || (len(eg.AllowHosts) == 0 && len(eg.AllowCIDRs) == 0) {
		return false, map[string]any{"denyOut": []string{"0.0.0.0/0"}}
	}
	allow := append(sortedCopy(eg.AllowHosts), sortedCopy(eg.AllowCIDRs)...)
	// Allow rules win over deny rules: everything else is denied.
	return true, map[string]any{"allowOut": allow, "denyOut": []string{"0.0.0.0/0"}}
}

// Create implements Platform.
func (e *E2B) Create(ctx context.Context, s Spec) (*Box, error) {
	cpus := ceilInt(s.CPUs)
	if cpus < 1 {
		cpus = 2
	}
	memMB := int(s.MemGB * 1024)
	if memMB < 512 {
		memMB = 2048
	}
	tid, err := e.ensureTemplate(ctx, s.Image, cpus, memMB)
	if err != nil {
		return nil, err
	}
	internet, network := e2bNetwork(s.Egress)
	body := map[string]any{"templateID": tid, "timeout": lifetimeSeconds(s), "autoPause": false,
		"allow_internet_access": internet, "network": network}
	if len(s.Labels) > 0 {
		body["metadata"] = s.Labels
	}
	if len(s.Env) > 0 {
		body["envVars"] = s.Env
	}
	var sb struct {
		SandboxID       string `json:"sandboxID"`
		EnvdAccessToken string `json:"envdAccessToken"`
	}
	if err := e.api.json(ctx, "create sandbox", http.MethodPost, "/v2/sandboxes", body, &sb); err != nil {
		return nil, err
	}
	if sb.SandboxID == "" {
		return nil, errors.New("E2B create sandbox: the answer has no sandbox id")
	}
	return &Box{ID: sb.SandboxID, Platform: PlatformE2B, Data: map[string]string{"envd_token": sb.EnvdAccessToken}}, nil
}

// envd returns an envd client for the sandbox.
func (e *E2B) envd(b *Box) (*apiClient, error) {
	if b == nil || b.ID == "" {
		return nil, errors.New("E2B: no sandbox")
	}
	h := map[string]string{
		"E2b-Sandbox-Id":   b.ID,
		"E2b-Sandbox-Port": e2bEnvdPort,
		// The system user commands and files run as.
		"Authorization": "Basic " + base64.StdEncoding.EncodeToString([]byte("root:")),
	}
	secrets := append([]string(nil), e.api.secrets...)
	if t := b.Data["envd_token"]; t != "" {
		h["X-Access-Token"] = t
		secrets = append(secrets, t)
	}
	return &apiClient{http: e.http, base: e.envdBase, title: "E2B", headers: h, secrets: secrets}, nil
}

// connectFrame is one Connect protocol envelope: flags, length, payload.
func connectFrame(flags byte, payload []byte) []byte {
	out := make([]byte, 5+len(payload))
	out[0] = flags
	binary.BigEndian.PutUint32(out[1:5], uint32(len(payload)))
	copy(out[5:], payload)
	return out
}

// readFrame reads one envelope.
func readFrame(r io.Reader) (byte, []byte, error) {
	var h [5]byte
	if _, err := io.ReadFull(r, h[:]); err != nil {
		return 0, nil, err
	}
	n := binary.BigEndian.Uint32(h[1:5])
	if n > e2bMaxFrame {
		return 0, nil, fmt.Errorf("frame of %d bytes is over the cap", n)
	}
	p := make([]byte, n)
	if _, err := io.ReadFull(r, p); err != nil {
		return 0, nil, err
	}
	return h[0], p, nil
}

// e2bEvent is process.StartResponse in protobuf JSON: bytes are base64,
// zero values are left out (an exit code of 0 is absent).
type e2bEvent struct {
	Event *struct {
		Start *struct {
			PID uint32 `json:"pid"`
		} `json:"start"`
		Data *struct {
			Stdout []byte `json:"stdout"`
			Stderr []byte `json:"stderr"`
		} `json:"data"`
		End *struct {
			ExitCode int    `json:"exitCode"`
			Exited   bool   `json:"exited"`
			Status   string `json:"status"`
			Error    string `json:"error"`
		} `json:"end"`
	} `json:"event"`
	Error *struct {
		Code    string `json:"code"`
		Message string `json:"message"`
	} `json:"error"`
}

// Exec implements Platform.
func (e *E2B) Exec(ctx context.Context, b *Box, c Command) (*Result, error) {
	cl, err := e.envd(b)
	if err != nil {
		return nil, err
	}
	if len(c.Argv) == 0 {
		return nil, errors.New("E2B run command: empty command")
	}
	to := commandTimeout(c)
	cctx, cancel := context.WithTimeout(ctx, to+30*time.Second)
	defer cancel()
	proc := map[string]any{"cmd": c.Argv[0], "args": c.Argv[1:]}
	if len(c.Env) > 0 {
		proc["envs"] = c.Env
	}
	if c.Cwd != "" {
		proc["cwd"] = c.Cwd
	}
	payload, err := json.Marshal(map[string]any{"process": proc, "stdin": false})
	if err != nil {
		return nil, err
	}
	req, err := cl.request(cctx, http.MethodPost, "/process.Process/Start", bytes.NewReader(connectFrame(0, payload)), "application/connect+json")
	if err != nil {
		return nil, cl.wrap("run command", err)
	}
	req.Header.Set("Connect-Protocol-Version", "1")
	req.Header.Set("Connect-Timeout-Ms", strconv.FormatInt(to.Milliseconds(), 10))
	resp, err := cl.client().Do(req)
	if err != nil {
		return nil, cl.wrap("run command", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, maxErrorBody))
		return nil, cl.wrap("run command", &HTTPError{Status: resp.StatusCode, Body: strings.TrimSpace(string(body))})
	}
	var out bytes.Buffer
	res := &Result{}
	ended := false
	br := bufio.NewReader(resp.Body)
	for {
		flags, p, err := readFrame(br)
		if err != nil {
			return nil, cl.wrap("run command", fmt.Errorf("read stream: %w", err))
		}
		var ev e2bEvent
		if len(p) > 0 {
			if err := json.Unmarshal(p, &ev); err != nil {
				return nil, cl.wrap("run command", fmt.Errorf("decode stream: %w", err))
			}
		}
		if flags&2 != 0 { // end of stream
			if ev.Error != nil {
				return nil, cl.wrap("run command", fmt.Errorf("%s: %s", ev.Error.Code, ev.Error.Message))
			}
			break
		}
		if ev.Event == nil {
			continue
		}
		if d := ev.Event.Data; d != nil {
			for _, chunk := range [][]byte{d.Stdout, d.Stderr} {
				if room := e2bOutputCap - out.Len(); room > 0 {
					if len(chunk) > room {
						chunk = chunk[:room]
					}
					out.Write(chunk)
				}
			}
		}
		if end := ev.Event.End; end != nil {
			ended = true
			res.ExitCode = end.ExitCode
			if !end.Exited && end.ExitCode == 0 {
				res.ExitCode = -1 // killed by a signal
			}
		}
	}
	if !ended {
		return nil, errors.New("E2B run command: the stream ended without the process end")
	}
	res.Output = out.Bytes()
	return res, nil
}

func filesPath(path string) string {
	return "/files?path=" + url.QueryEscape(path) + "&username=root"
}

// Upload implements Platform.
func (e *E2B) Upload(ctx context.Context, b *Box, path string, data []byte) error {
	cl, err := e.envd(b)
	if err != nil {
		return err
	}
	_, err = cl.raw(ctx, "upload "+path, http.MethodPost, filesPath(path), data, maxAPIBody)
	return err
}

// Download implements Platform.
func (e *E2B) Download(ctx context.Context, b *Box, path string, max int64) ([]byte, error) {
	cl, err := e.envd(b)
	if err != nil {
		return nil, err
	}
	return cl.raw(ctx, "download "+path, http.MethodGet, filesPath(path), nil, max)
}

// Destroy implements Platform.
func (e *E2B) Destroy(ctx context.Context, b *Box) error {
	if b == nil || b.ID == "" {
		return nil
	}
	err := e.api.json(ctx, "kill sandbox", http.MethodDelete, "/sandboxes/"+url.PathEscape(b.ID), nil, nil)
	if errors.Is(err, ErrNotFound) {
		return nil
	}
	return err
}
