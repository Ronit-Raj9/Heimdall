package environments

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/maximhq/bifrost/bench/utils"
)

type adapterFaultController struct{ env *dockerWorkspace }

// NewAdapterFaultController isolates hook decisions and ledger files from the
// agent. Resources are per run and have the standard janitor labels.
func NewAdapterFaultController(cfg DockerWorkspaceConfig) schemas.AdapterFaultController {
	return &adapterFaultController{env: newDockerWorkspace(cfg)}
}
func (c *adapterFaultController) GetName() string    { return "adapter_faults" }
func (c *adapterFaultController) GetVersion() string { return "1.0.0" }
func hookContainer(id string) string                 { return "bench-hf-" + id }
func hookNetwork(id string) string                   { return "bench-hf-net-" + id }
func hookConfigVolume(id string) string              { return "bench-hf-cfg-" + id }
func hookLedgerVolume(id string) string              { return "bench-hf-ledger-" + id }
func hookToken() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

func (c *adapterFaultController) Arm(ctx context.Context, rc *schemas.RunContext) error {
	expID, id, err := runIDs(rc)
	if err != nil {
		return err
	}
	if rc.Probe == nil || rc.Probe.Apply.AdapterFault == nil || rc.Agent == nil || rc.Agent.AdapterCaps == nil {
		return fmt.Errorf("adapter fault configuration missing")
	}
	wall := rc.Agent.Budgets.WallS
	if wall <= 0 {
		wall = defaultWallS
	}
	rule := *rc.Probe.Apply.AdapterFault
	if err := utils.ValidateAdapterFault(rule, wall*1000); err != nil {
		return err
	}
	if err := utils.ValidateAdapterFaultHooks(rc.Agent.AdapterCaps.FaultHooks, rule); err != nil {
		return err
	}
	token, err := hookToken()
	if err != nil {
		return err
	}
	admin, err := hookToken()
	if err != nil {
		return err
	}
	nonce, err := hookToken()
	if err != nil {
		return err
	}
	cfg := schemas.AdapterFaultConfig{RunID: id, Token: token, AdminToken: admin, Nonce: nonce, Rule: rule, Hooks: rc.Agent.AdapterCaps.FaultHooks, WallMS: wall * 1000, Sessions: []string{"default"}}
	if rc.Task != nil && len(rc.Task.Sessions) > 0 {
		cfg.Sessions = nil
		for _, s := range rc.Task.Sessions {
			cfg.Sessions = append(cfg.Sessions, s.ID)
		}
	}
	// Set the handle before allocation so cleanup covers a partially armed run.
	rc.FaultRuntime = &schemas.AdapterFaultRuntime{URL: "http://hookfault:9300", Token: token, Network: hookNetwork(id), Nonce: nonce}
	args := append([]string{"network", "create", "--internal"}, utils.LabelArgs(expID, id)...)
	args = append(args, hookNetwork(id))
	if _, err := c.env.cfg.Docker.MustRun(ctx, args...); err != nil {
		return err
	}
	for _, v := range []string{hookConfigVolume(id), hookLedgerVolume(id)} {
		if err := c.env.freshVolume(ctx, v, expID, id); err != nil {
			return err
		}
	}
	image := c.env.toolProxyImage()
	info, err := InspectImage(ctx, c.env.cfg.Docker, image)
	if err != nil {
		return err
	}
	if err := c.env.ownVolumes(ctx, rc, expID, id, info.User, hookLedgerVolume(id)); err != nil {
		return err
	}
	args = append([]string{"create", "--name", hookContainer(id)}, utils.LabelArgs(expID, id)...)
	args = append(args, "--network", hookNetwork(id), "--network-alias", "hookfault", "--cap-drop", "ALL", "--security-opt", "no-new-privileges", "--read-only", "--tmpfs", "/tmp:size=16m", "--pids-limit", "64", "--memory", "256m", "--cpus", "1", "--log-driver", "none", "-v", hookConfigVolume(id)+":/cfg", "-v", hookLedgerVolume(id)+":/ledger", "--entrypoint", "bifrost-bench", image, "hookfault")
	if _, err := c.env.cfg.Docker.MustRun(ctx, args...); err != nil {
		return err
	}
	raw, err := json.Marshal(cfg)
	if err != nil {
		return err
	}
	if err := c.env.copyBytes(ctx, hookContainer(id), "/cfg", "runtime.json", raw); err != nil {
		return err
	}
	if _, err := c.env.cfg.Docker.MustRun(ctx, "start", hookContainer(id)); err != nil {
		return err
	}
	ready := schemas.ServiceSpec{ID: "hookfault", Ready: &schemas.ReadyCheck{Argv: []string{"bifrost-bench", "hookfault", "-action", "health"}, TimeoutS: 15}}
	return c.env.waitReady(ctx, hookContainer(id), ready, "")
}

func (c *adapterFaultController) Freeze(ctx context.Context, rc *schemas.RunContext) error {
	if rc == nil || rc.FaultRuntime == nil || rc.Run == nil {
		return fmt.Errorf("adapter fault controller was not armed")
	}
	id := rc.Run.RunID
	raw, err := json.Marshal(rc.FaultSessions)
	if err != nil {
		return err
	}
	if _, err := c.env.cfg.Docker.MustRun(ctx, "exec", hookContainer(id), "bifrost-bench", "hookfault", "-action", "freeze", "-sessions", string(raw)); err != nil {
		return err
	}
	for _, name := range []string{"adapter.fault.json", "adapter.fault.jsonl"} {
		// docker cp, with the docker exec tar fallback under gVisor (copyout.go).
		if err := c.env.copyFileOut(ctx, hookContainer(id), "/ledger/"+name, filepath.Join(rc.Dir, name), utils.AdapterFaultMaxLedger+(4<<20)); err != nil {
			return err
		}
		// Controller export is bounded before it is served or sealed.
		st, err := os.Stat(filepath.Join(rc.Dir, name))
		if err != nil {
			return err
		}
		if st.Size() > utils.AdapterFaultMaxLedger+(4<<20) {
			return fmt.Errorf("adapter fault export exceeds cap")
		}
	}
	return nil
}

func (c *adapterFaultController) Cleanup(ctx context.Context, rc *schemas.RunContext) error {
	if rc == nil || rc.FaultRuntime == nil || rc.Run == nil {
		return nil
	}
	id := rc.Run.RunID
	var errs []error
	for _, args := range [][]string{{"rm", "-f", hookContainer(id)}, {"volume", "rm", hookLedgerVolume(id), hookConfigVolume(id)}, {"network", "rm", hookNetwork(id)}} {
		r, err := c.env.cfg.Docker.Run(ctx, args...)
		if err != nil {
			errs = append(errs, err)
		} else if r.ExitCode != 0 {
			// Docker's missing resource status is expected after an earlier cleanup.
			if !strings.Contains(strings.ToLower(string(r.Stderr)), "no such") {
				errs = append(errs, fmt.Errorf("cleanup hook controller: %s", r.Stderr))
			}
		}
	}
	return errors.Join(errs...)
}
