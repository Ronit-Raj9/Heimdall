package sandbox

import (
	"context"
	"errors"
	"fmt"
	"math"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"
)

// Daytona (https://www.daytona.io/docs, OpenAPI at /docs/openapi.json and
// /docs/toolbox-openapi.json). The main API creates, reads and deletes
// sandboxes; each sandbox's toolbox, reached through the toolbox proxy URL
// the sandbox reports, runs commands and moves files. Both take the
// organization's API key as a bearer token.
//
//	POST   {api}/sandbox                          create (image as a one-line Dockerfile)
//	GET    {api}/sandbox/{id}                     state: started | error | build_failed | ...
//	DELETE {api}/sandbox/{id}                     delete
//	POST   {toolbox}/{id}/process/execute         run a command, returns exitCode and result
//	POST   {toolbox}/{id}/files/upload-v2?path=   raw body upload
//	GET    {toolbox}/{id}/files/download?path=    download
const (
	DaytonaAPI = "https://app.daytona.io/api"

	daytonaStartTimeout = 15 * time.Minute // a first build of the image may take minutes
	daytonaOutputCap    = 1 << 20
)

// daytonaPollEvery is how often Create reads the sandbox state.
var daytonaPollEvery = 2 * time.Second

func init() {
	Register(Info{
		Name: PlatformDaytona, Title: "Daytona",
		TokenHint:           "An API key from the Daytona dashboard (Keys), with sandbox write and delete permissions.",
		EndpointLabel:       "API URL (only for a self-hosted Daytona)",
		EndpointPlaceholder: "https://daytona.your-company.com/api",
		DocsURL:             "https://www.daytona.io/docs/en/api-keys",
	}, func(c Credentials) (Platform, error) { return NewDaytona(c, nil), nil })
}

// Daytona is the Daytona client.
type Daytona struct {
	api *apiClient
}

// NewDaytona returns a Daytona client; hc nil uses the default client.
func NewDaytona(c Credentials, hc *http.Client) *Daytona {
	base := strings.TrimSpace(c.Endpoint)
	if base == "" {
		base = DaytonaAPI
	}
	tok := strings.TrimSpace(c.Token)
	return &Daytona{api: &apiClient{http: hc, base: base, title: "Daytona",
		headers: map[string]string{"Authorization": "Bearer " + tok}, secrets: []string{tok}}}
}

// Name implements Platform.
func (d *Daytona) Name() string { return PlatformDaytona }

// Check implements Platform: list sandboxes, the cheapest authenticated read.
func (d *Daytona) Check(ctx context.Context) error {
	return d.api.json(ctx, "check token", http.MethodGet, "/sandbox", nil, nil)
}

type daytonaBuildInfo struct {
	DockerfileContent string `json:"dockerfileContent"`
}

type daytonaCreate struct {
	Name               string            `json:"name,omitempty"`
	User               string            `json:"user,omitempty"`
	Env                map[string]string `json:"env,omitempty"`
	Labels             map[string]string `json:"labels,omitempty"`
	Public             bool              `json:"public"`
	CPU                int               `json:"cpu,omitempty"`
	Memory             int               `json:"memory,omitempty"`
	Disk               int               `json:"disk,omitempty"`
	AutoStopInterval   int               `json:"autoStopInterval"`
	AutoDeleteInterval int               `json:"autoDeleteInterval"`
	NetworkBlockAll    bool              `json:"networkBlockAll"`
	NetworkAllowList   string            `json:"networkAllowList,omitempty"`
	DomainAllowList    string            `json:"domainAllowList,omitempty"`
	BuildInfo          *daytonaBuildInfo `json:"buildInfo,omitempty"`
}

type daytonaSandbox struct {
	ID              string `json:"id"`
	State           string `json:"state"`
	ErrorReason     string `json:"errorReason"`
	ToolboxProxyURL string `json:"toolboxProxyUrl"`
}

// daytonaBody is the create request for s.
func daytonaBody(s Spec) daytonaCreate {
	minutes := int(math.Ceil(float64(lifetimeSeconds(s)) / 60))
	body := daytonaCreate{
		Name: s.Name, User: "root", Env: s.Env, Labels: s.Labels,
		CPU: ceilInt(s.CPUs), Memory: ceilInt(s.MemGB), Disk: s.DiskGB,
		// Stopped after the lifetime and deleted as soon as it stops: a
		// sandbox this process lost track of cannot outlive its run.
		AutoStopInterval: minutes, AutoDeleteInterval: 0,
		BuildInfo: &daytonaBuildInfo{DockerfileContent: "FROM " + s.Image + "\n"},
	}
	switch {
	case s.Egress.BlockAll:
		body.NetworkBlockAll = true
	default:
		// The allow lists replace the default open network: with both
		// empty, nothing is reachable, which networkBlockAll says plainly.
		hosts, cidrs := sortedCopy(s.Egress.AllowHosts), sortedCopy(s.Egress.AllowCIDRs)
		if len(hosts) == 0 && len(cidrs) == 0 {
			body.NetworkBlockAll = true
		}
		body.NetworkAllowList = strings.Join(cidrs, ",")
		body.DomainAllowList = strings.Join(hosts, ",")
	}
	return body
}

// Create implements Platform.
func (d *Daytona) Create(ctx context.Context, s Spec) (*Box, error) {
	var sb daytonaSandbox
	if err := d.api.json(ctx, "create sandbox", http.MethodPost, "/sandbox", daytonaBody(s), &sb); err != nil {
		return nil, err
	}
	if sb.ID == "" {
		return nil, errors.New("Daytona create sandbox: the answer has no sandbox id")
	}
	box := &Box{ID: sb.ID, Platform: PlatformDaytona, Data: map[string]string{}}
	wctx, cancel := context.WithTimeout(ctx, daytonaStartTimeout)
	defer cancel()
	err := poll(wctx, daytonaPollEvery, func() (bool, error) {
		switch sb.State {
		case "started":
			return true, nil
		case "error", "build_failed", "destroyed", "destroying":
			return false, fmt.Errorf("Daytona sandbox %s is %s: %s", sb.ID, sb.State, sb.ErrorReason)
		}
		return false, d.api.json(wctx, "read sandbox", http.MethodGet, "/sandbox/"+url.PathEscape(sb.ID), nil, &sb)
	})
	if err != nil {
		_ = d.Destroy(context.WithoutCancel(ctx), box)
		return nil, err
	}
	tb := strings.TrimRight(sb.ToolboxProxyURL, "/")
	if tb == "" {
		var u struct {
			URL string `json:"url"`
		}
		if err := d.api.json(ctx, "toolbox URL", http.MethodGet, "/sandbox/"+url.PathEscape(sb.ID)+"/toolbox-proxy-url", nil, &u); err != nil {
			_ = d.Destroy(context.WithoutCancel(ctx), box)
			return nil, err
		}
		tb = strings.TrimRight(u.URL, "/")
	}
	if tb == "" {
		_ = d.Destroy(context.WithoutCancel(ctx), box)
		return nil, errors.New("Daytona sandbox has no toolbox URL")
	}
	box.Data["toolbox"] = tb + "/" + url.PathEscape(sb.ID)
	return box, nil
}

func (d *Daytona) toolbox(b *Box) (string, error) {
	if b == nil || b.Data["toolbox"] == "" {
		return "", errors.New("Daytona: sandbox has no toolbox URL")
	}
	return b.Data["toolbox"], nil
}

// Exec implements Platform.
func (d *Daytona) Exec(ctx context.Context, b *Box, c Command) (*Result, error) {
	tb, err := d.toolbox(b)
	if err != nil {
		return nil, err
	}
	to := commandTimeout(c)
	cctx, cancel := context.WithTimeout(ctx, to+30*time.Second)
	defer cancel()
	in := map[string]any{"command": shellJoin(c.Argv), "timeout": int(math.Ceil(to.Seconds()))}
	if len(c.Env) > 0 {
		in["envs"] = c.Env
	}
	if c.Cwd != "" {
		in["cwd"] = c.Cwd
	}
	var out struct {
		ExitCode *int   `json:"exitCode"`
		Result   string `json:"result"`
	}
	if err := d.api.json(cctx, "run command", http.MethodPost, tb+"/process/execute", in, &out); err != nil {
		return nil, err
	}
	res := &Result{Output: []byte(out.Result)}
	if len(res.Output) > daytonaOutputCap {
		res.Output = res.Output[:daytonaOutputCap]
	}
	if out.ExitCode == nil {
		return nil, errors.New("Daytona run command: the answer has no exit code")
	}
	res.ExitCode = *out.ExitCode
	return res, nil
}

// Upload implements Platform.
func (d *Daytona) Upload(ctx context.Context, b *Box, path string, data []byte) error {
	tb, err := d.toolbox(b)
	if err != nil {
		return err
	}
	_, err = d.api.raw(ctx, "upload "+path, http.MethodPost, tb+"/files/upload-v2?path="+url.QueryEscape(path), data, maxAPIBody)
	return err
}

// Download implements Platform.
func (d *Daytona) Download(ctx context.Context, b *Box, path string, max int64) ([]byte, error) {
	tb, err := d.toolbox(b)
	if err != nil {
		return nil, err
	}
	return d.api.raw(ctx, "download "+path, http.MethodGet, tb+"/files/download?path="+url.QueryEscape(path), nil, max)
}

// Destroy implements Platform.
func (d *Daytona) Destroy(ctx context.Context, b *Box) error {
	if b == nil || b.ID == "" {
		return nil
	}
	err := d.api.json(ctx, "delete sandbox", http.MethodDelete, "/sandbox/"+url.PathEscape(b.ID), nil, nil)
	if errors.Is(err, ErrNotFound) {
		return nil
	}
	return err
}

func ceilInt(f float64) int {
	if f <= 0 {
		return 0
	}
	return int(math.Ceil(f))
}

func sortedCopy(in []string) []string {
	out := append([]string(nil), in...)
	sort.Strings(out)
	return out
}
