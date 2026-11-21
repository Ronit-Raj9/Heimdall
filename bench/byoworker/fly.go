package byoworker

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"time"
)

// Fly Machines (https://docs.fly.io/machines/api, REST API at
// https://api.machines.dev, bearer token). A worker is one Machine in an app
// the org created for it (`fly apps create`); the Machine is a Firecracker
// VM, so Docker runs inside it for the agent containers.
//
//	GET    /v1/apps/{app}                          credential and app check
//	POST   /v1/apps/{app}/machines                 create (name, region, config.image,
//	                                               config.env, config.init, config.guest,
//	                                               config.restart)
//	GET    /v1/apps/{app}/machines/{id}            status
//	DELETE /v1/apps/{app}/machines/{id}?force=true stop and remove

// FlyBaseURL is the public Machines API.
const FlyBaseURL = "https://api.machines.dev"

// Fly settings.
const (
	FlyApp      = "app"       // required: the Fly app the Machines go in
	FlyRegion   = "region"    // optional: e.g. iad, fra; default Fly's choice
	FlyCPUs     = "cpus"      // 1 to 16, default 4
	FlyMemoryMB = "memory_mb" // 1024 to 131072, default 8192
	FlyCPUKind  = "cpu_kind"  // shared or performance, default performance
	FlyImage    = "image"     // optional: an image with the runner preinstalled; default FlyDefaultImage
)

// FlyDefaultImage is the Machine image when the org names none: plain
// Ubuntu; the boot line installs Docker and the runner.
const FlyDefaultImage = "ubuntu:24.04"

// Fly is the Fly Machines driver.
type Fly struct {
	// BaseURL is the API address; "" is FlyBaseURL. Set by tests only.
	BaseURL string
	HTTP    *http.Client
	Now     func() time.Time
}

// Platform implements Driver.
func (Fly) Platform() string { return PlatformFly }

// Validate implements Driver.
func (Fly) Validate(in map[string]string, secret string) (map[string]string, error) {
	s, err := settingsOnly(in, FlyApp, FlyRegion, FlyCPUs, FlyMemoryMB, FlyCPUKind, FlyImage)
	if err != nil {
		return nil, err
	}
	if !validFlyApp(s[FlyApp]) {
		return nil, fmt.Errorf("%w: app must be the name of a Fly app you created for workers (fly apps create), lowercase letters, digits and '-'", ErrInvalid)
	}
	if r := s[FlyRegion]; r != "" && (len(r) != 3 || !validFlyApp(r)) {
		return nil, fmt.Errorf("%w: region must be a three letter Fly region code such as iad or fra", ErrInvalid)
	}
	if err := intSetting(s, FlyCPUs, 4, 1, 16); err != nil {
		return nil, err
	}
	if err := intSetting(s, FlyMemoryMB, 8192, 1024, 131072); err != nil {
		return nil, err
	}
	switch s[FlyCPUKind] {
	case "":
		s[FlyCPUKind] = "performance"
	case "shared", "performance":
	default:
		return nil, fmt.Errorf("%w: cpu_kind must be shared or performance", ErrInvalid)
	}
	if img := s[FlyImage]; img != "" && !validImage(img) {
		return nil, fmt.Errorf("%w: image must be an image reference such as registry.fly.io/my-runner:1", ErrInvalid)
	}
	if len(secret) < 16 || len(secret) > 4096 {
		return nil, fmt.Errorf("%w: paste a Fly token for the app (fly tokens create deploy -a <app>)", ErrInvalid)
	}
	return s, nil
}

func (f Fly) client(t Target) *apiClient {
	base := f.BaseURL
	if base == "" {
		base = FlyBaseURL
	}
	return newAPIClient(base, t.Secret, f.HTTP)
}

func (f Fly) now() time.Time {
	if f.Now != nil {
		return f.Now()
	}
	return time.Now()
}

func flyCapacity(s map[string]string) string {
	mem, _ := strconv.Atoi(s[FlyMemoryMB])
	return fmt.Sprintf("Fly Machine: %s CPU (%s), %s memory", s[FlyCPUs], s[FlyCPUKind], gb(mem))
}

func gb(mb int) string {
	if mb%1024 == 0 {
		return strconv.Itoa(mb/1024) + " GB"
	}
	return strconv.Itoa(mb) + " MB"
}

// Check implements Driver: reads the app.
func (f Fly) Check(ctx context.Context, t Target) Health {
	app := t.Settings[FlyApp]
	var out struct {
		Name string `json:"name"`
	}
	err := f.client(t).do(ctx, http.MethodGet, "/v1/apps/"+url.PathEscape(app), nil, &out)
	if isStatus(err, http.StatusNotFound) {
		err = fmt.Errorf("app %s does not exist for this token; create it with fly apps create %s", app, app)
	}
	return checkResult(f.now(), err, "Fly accepted the token for app "+app+".", flyCapacity(t.Settings))
}

type flyMachine struct {
	ID    string `json:"id"`
	State string `json:"state"`
}

func (m flyMachine) instance() Instance {
	in := Instance{ID: m.ID, Detail: m.State}
	switch m.State {
	case "created", "starting", "replacing", "updating":
		in.State = InstanceStarting
	case "started":
		in.State = InstanceRunning
	case "stopping", "stopped", "suspending", "suspended", "destroying", "destroyed":
		in.State = InstanceStopped
	case "failed":
		in.State = InstanceFailed
	default:
		in.State = InstanceUnknown
	}
	return in
}

// Start implements Driver: one Machine whose process installs and runs the
// runner, restarted when it exits.
func (f Fly) Start(ctx context.Context, t Target, b Boot) (Instance, error) {
	if err := checkBoot(b); err != nil {
		return Instance{}, err
	}
	cpus, _ := strconv.Atoi(t.Settings[FlyCPUs])
	mem, _ := strconv.Atoi(t.Settings[FlyMemoryMB])
	image := t.Settings[FlyImage]
	if image == "" {
		image = b.Image
	}
	if image == "" {
		image = FlyDefaultImage
	}
	body := map[string]any{
		"name": b.Name,
		"config": map[string]any{
			"image": image,
			"env":   BootEnv(b, PlatformFly),
			"init": map[string]any{
				"entrypoint": []string{"/bin/sh", "-c"},
				"cmd":        []string{BootScript()},
			},
			"guest":    map[string]any{"cpus": cpus, "memory_mb": mem, "cpu_kind": t.Settings[FlyCPUKind]},
			"restart":  map[string]any{"policy": "always"},
			"metadata": map[string]string{"heimdall_pool": b.Pool, "heimdall_worker": b.Name},
		},
	}
	if r := t.Settings[FlyRegion]; r != "" {
		body["region"] = r
	}
	var out flyMachine
	if err := f.client(t).do(ctx, http.MethodPost, "/v1/apps/"+url.PathEscape(t.Settings[FlyApp])+"/machines", body, &out); err != nil {
		return Instance{}, fmt.Errorf("create the Fly Machine: %w", err)
	}
	if out.ID == "" {
		return Instance{}, errors.New("create the Fly Machine: the answer has no machine id")
	}
	return out.instance(), nil
}

func (f Fly) machinePath(t Target, id string) string {
	return "/v1/apps/" + url.PathEscape(t.Settings[FlyApp]) + "/machines/" + url.PathEscape(id)
}

// Status implements Driver.
func (f Fly) Status(ctx context.Context, t Target, id string) (Instance, error) {
	var out flyMachine
	if err := f.client(t).do(ctx, http.MethodGet, f.machinePath(t, id), nil, &out); err != nil {
		if isStatus(err, http.StatusNotFound) {
			return Instance{ID: id, State: InstanceStopped, Detail: "the Machine no longer exists"}, nil
		}
		return Instance{}, fmt.Errorf("read the Fly Machine: %w", err)
	}
	return out.instance(), nil
}

// Stop implements Driver: force-removes the Machine.
func (f Fly) Stop(ctx context.Context, t Target, id string) error {
	err := f.client(t).do(ctx, http.MethodDelete, f.machinePath(t, id)+"?force=true", nil, nil)
	if err != nil && !isStatus(err, http.StatusNotFound) {
		return fmt.Errorf("remove the Fly Machine: %w", err)
	}
	return nil
}

// validFlyApp: lowercase letters, digits and '-', 1 to 63.
func validFlyApp(s string) bool {
	if s == "" || len(s) > 63 {
		return false
	}
	for _, c := range s {
		if !(c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || c == '-') {
			return false
		}
	}
	return true
}

// validImage accepts a container image reference without spaces or shell
// characters.
func validImage(s string) bool {
	if s == "" || len(s) > 255 {
		return false
	}
	for _, c := range s {
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '.' || c == '_' || c == '-' || c == '/' || c == ':' || c == '@') {
			return false
		}
	}
	return true
}

// intSetting fills a default or checks an integer setting's range.
func intSetting(s map[string]string, key string, def, lo, hi int) error {
	v := s[key]
	if v == "" {
		s[key] = strconv.Itoa(def)
		return nil
	}
	n, err := strconv.Atoi(v)
	if err != nil || n < lo || n > hi {
		return fmt.Errorf("%w: %s must be %d to %d", ErrInvalid, key, lo, hi)
	}
	s[key] = strconv.Itoa(n)
	return nil
}
