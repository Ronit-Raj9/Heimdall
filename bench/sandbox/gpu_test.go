package sandbox

import (
	"context"
	"strings"
	"testing"

	"github.com/maximhq/bifrost/bench/environments"
	"github.com/maximhq/bifrost/bench/schemas"
)

func TestDeclaredGPUsRoundTrip(t *testing.T) {
	l4, ok := LookupModalGPU("l4")
	if !ok {
		t.Fatal("L4 missing")
	}
	devs := DeclaredGPUs(PlatformModal, l4)
	if len(devs) != MaxGPUsPerSandbox || !IsDeclaredGPU(devs[0]) || devs[0].MemoryMB != l4.MemoryMB {
		t.Fatalf("devs = %+v", devs)
	}
	a100, _ := LookupModalGPU("A100-80GB")
	if got := declaredType(DeclaredGPUs(PlatformModal, a100)[3]); got != "A100-80GB" {
		t.Fatalf("type = %q", got)
	}
	if _, ok := LookupModalGPU("A10G"); !ok {
		t.Fatal("A10G alias")
	}
}

func TestGPUTypeFor(t *testing.T) {
	big := &schemas.GPURequest{Count: 1, MinVRAMMB: 40000}
	g, err := gpuTypeFor(ModalGPUs, "", nil, big)
	if err != nil || g.Name != "L40S" {
		t.Fatalf("cheapest 40 GB = %v %v", g, err)
	}
	if _, err := gpuTypeFor(ModalGPUs, "L4", nil, big); err == nil || !strings.Contains(err.Error(), "40000 MiB") {
		t.Fatalf("configured L4 for 40 GB: %v", err)
	}
	h100, _ := LookupModalGPU("H100")
	a := &schemas.GPUAssignment{GPUs: DeclaredGPUs(PlatformModal, h100)[:2]}
	if g, err := gpuTypeFor(ModalGPUs, "L4", a, nil); err != nil || g.Name != "H100" {
		t.Fatalf("held type wins: %v %v", g, err)
	}
	if gpuSpec(h100, 2) != "H100:2" || gpuSpec(h100, 1) != "H100" {
		t.Fatal("gpuSpec")
	}
	if _, err := gpuTypeFor(ModalGPUs, "", nil, &schemas.GPURequest{MinVRAMMB: 1 << 30}); err == nil {
		t.Fatal("no GPU is that large")
	}
}

// TestRefusalsGPU: Modal takes GPU tasks it can place; a platform without
// GPU sandboxes refuses them.
func TestRefusalsGPU(t *testing.T) {
	task := &schemas.Task{ID: "k", Resources: &schemas.Resources{GPU: &schemas.GPURequest{Count: 1, MinVRAMMB: 8000}}}
	slot := environments.DriverSlot{Task: task}
	m, err := NewModal(Credentials{Token: "ak-x:as-y"}, "")
	if err != nil {
		t.Fatal(err)
	}
	has := func(why []string, part string) bool {
		for _, w := range why {
			if strings.Contains(w, part) {
				return true
			}
		}
		return false
	}
	d := &Driver{cfg: DriverConfig{Platform: m, PublicGatewayURL: "https://gw.example.com"}}
	if why := d.Refusals(slot); has(why, "GPU") {
		t.Fatalf("Modal refused a GPU task: %v", why)
	}
	d.cfg.GPUType = "T4"
	task.Resources.GPU.MinVRAMMB = 20000
	if why := d.Refusals(slot); !has(why, "20000 MiB") {
		t.Fatalf("T4 for 20 GB: %v", why)
	}
	task.Resources.GPU = &schemas.GPURequest{Count: 9}
	d.cfg.GPUType = ""
	if why := d.Refusals(slot); !has(why, "at most 8") {
		t.Fatalf("9 GPUs: %v", why)
	}
	d.cfg.Platform = noGPU{}
	task.Resources.GPU = &schemas.GPURequest{Count: 1}
	if why := d.Refusals(slot); !has(why, RefuseGPU) {
		t.Fatalf("no GPU platform: %v", why)
	}
}

type noGPU struct{ Platform }

func (noGPU) Name() string                  { return "nogpu" }
func (noGPU) Check(context.Context) error { return nil }
