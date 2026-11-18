package verdict

import (
	"strings"
	"testing"

	"github.com/maximhq/bifrost/bench/schemas"
)

// X7: the hardware parity instrument (hardwareparity.go).

func gpuRunOn(arm, task, name string, mem int) schemas.SealedRun {
	return schemas.SealedRun{
		Slot: schemas.Slot{Arm: arm, Task: task, Condition: "clean", Counted: true},
		Run:  schemas.Run{Hardware: &schemas.HardwareRecord{GPUs: []schemas.GPUInfo{{UUID: "GPU-" + name, Name: name, MemoryMB: mem}}}},
	}
}

func TestHardwareParity(t *testing.T) {
	plain := newRunIndex([]schemas.SealedRun{{Slot: schemas.Slot{Arm: "A", Task: "t", Condition: "clean", Counted: true}}})
	if _, ok := hardwareParity(plain); ok {
		t.Fatal("an experiment without GPU runs must get no hardware_parity check")
	}
	if got := withHardwareParity(nil, plain); len(got) != 0 {
		t.Fatal("no check appended without GPU runs")
	}
	same := newRunIndex([]schemas.SealedRun{gpuRunOn("A", "t1", "GTX 1650", 4096), gpuRunOn("B", "t1", "GTX 1650", 4096)})
	c, ok := hardwareParity(same)
	if !ok || c.Status != schemas.InstrumentPass || c.PerArm["A"].Num != 1 || c.PerArm["B"].Den != 1 {
		t.Fatalf("same model: %+v", c)
	}
	mixed := newRunIndex([]schemas.SealedRun{gpuRunOn("A", "t1", "GTX 1650", 4096), gpuRunOn("A", "t2", "GTX 1650", 4096), gpuRunOn("B", "t1", "RTX 4090", 24564)})
	c, ok = hardwareParity(mixed)
	if !ok || c.Status != schemas.InstrumentFail || !strings.Contains(c.Detail, "arm B: 1 on RTX 4090 24564 MiB") {
		t.Fatalf("mixed models: %+v", c)
	}
	if reasons := instrumentReasons([]schemas.InstrumentCheck{c}); len(reasons) != 1 {
		t.Error("a failed hardware_parity check must be an INVALID reason")
	}
	memory := newRunIndex([]schemas.SealedRun{gpuRunOn("A", "t1", "GTX 1650", 4096), gpuRunOn("B", "t1", "GTX 1650", 8192)})
	if c, _ := hardwareParity(memory); c.Status != schemas.InstrumentFail {
		t.Error("one card name with two memory sizes is two models")
	}
	uncounted := gpuRunOn("B", "t1", "RTX 4090", 24564)
	uncounted.Slot.Counted = false
	if c, _ := hardwareParity(newRunIndex([]schemas.SealedRun{gpuRunOn("A", "t1", "GTX 1650", 4096), uncounted})); c.Status != schemas.InstrumentPass {
		t.Error("compatibility runs never enter the comparison")
	}
}
