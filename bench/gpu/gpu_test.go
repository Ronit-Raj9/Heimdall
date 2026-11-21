package gpu

import (
	"context"
	"errors"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/maximhq/bifrost/bench/schemas"
)

// Output recorded from nvidia-smi 615.78.08 on a GTX 1650 (18 Nov 2026).
const (
	inventoryCSV = "0, GPU-9434bd05-0150-8ea1-ba79-6cbe56a59e15, NVIDIA GeForce GTX 1650, 4096, 615.78.08, 7.5\n"
	bannerText   = "| NVIDIA-SMI 615.78.08              KMD Version: 615.78.08     CUDA UMD Version: 13.4     |\n"
	listText     = "GPU 0: NVIDIA GeForce GTX 1650 (UUID: GPU-9434bd05-0150-8ea1-ba79-6cbe56a59e15)\n"
	gtxUUID      = "GPU-9434bd05-0150-8ea1-ba79-6cbe56a59e15"
)

func gtx(i int, uuid string, mem int) schemas.GPUInfo {
	return schemas.GPUInfo{Index: i, UUID: uuid, Name: "NVIDIA GeForce GTX 1650", MemoryMB: mem, Driver: "615.78.08", ComputeCap: "7.5"}
}

func TestDetect(t *testing.T) {
	run := func(_ context.Context, name string, args ...string) ([]byte, error) {
		if len(args) == 0 {
			return []byte(bannerText), nil
		}
		return []byte(inventoryCSV), nil
	}
	got, err := Detect(context.Background(), "", run)
	if err != nil {
		t.Fatal(err)
	}
	want := gtx(0, gtxUUID, 4096)
	want.CUDAVersion = "13.4"
	if len(got) != 1 || got[0] != want {
		t.Fatalf("Detect = %+v, want %+v", got, want)
	}
	old := strings.Replace(bannerText, "CUDA UMD Version", "CUDA Version", 1)
	run2 := func(_ context.Context, _ string, args ...string) ([]byte, error) {
		if len(args) == 0 {
			return []byte(old), nil
		}
		return []byte(inventoryCSV), nil
	}
	if got, _ := Detect(context.Background(), "", run2); got[0].CUDAVersion != "13.4" {
		t.Errorf("older banner: cuda %q, want 13.4", got[0].CUDAVersion)
	}
	fail := func(context.Context, string, ...string) ([]byte, error) { return nil, ErrNoSMI }
	if _, err := Detect(context.Background(), "", fail); !errors.Is(err, ErrNoSMI) {
		t.Errorf("no nvidia-smi: err %v, want ErrNoSMI", err)
	}
}

func TestParseInventoryRefusesMalformed(t *testing.T) {
	for _, in := range []string{"0, GPU-x, name, 4096, 615", "x, GPU-a, n, 1, d, 7.5", "0, abc, n, 1, d, 7.5", "0, GPU-a, n, lots, d, 7.5"} {
		if _, err := ParseInventory(in); err == nil {
			t.Errorf("ParseInventory(%q) accepted", in)
		}
	}
	two := "1, GPU-b, B, 8192, d, 8.6\n0, GPU-a, A, 4096, d, 7.5\n"
	got, err := ParseInventory(two)
	if err != nil || len(got) != 2 || got[0].UUID != "GPU-a" || got[1].MemoryMB != 8192 {
		t.Fatalf("ParseInventory sorts by index: %+v %v", got, err)
	}
}

func TestSelect(t *testing.T) {
	all := []schemas.GPUInfo{gtx(0, "GPU-a", 4096), gtx(1, "GPU-b", 8192)}
	cases := []struct {
		devices []string
		want    []string
		err     bool
	}{
		{nil, []string{"GPU-a", "GPU-b"}, false},
		{[]string{"all"}, []string{"GPU-a", "GPU-b"}, false},
		{[]string{"1"}, []string{"GPU-b"}, false},
		{[]string{"GPU-A", "0"}, []string{"GPU-a"}, false},
		{[]string{"2"}, nil, true},
	}
	for _, c := range cases {
		got, err := Select(all, c.devices)
		if (err != nil) != c.err {
			t.Errorf("Select(%v) err %v", c.devices, err)
			continue
		}
		var ids []string
		for _, g := range got {
			ids = append(ids, g.UUID)
		}
		if !c.err && !reflect.DeepEqual(ids, c.want) {
			t.Errorf("Select(%v) = %v, want %v", c.devices, ids, c.want)
		}
	}
}

func TestFits(t *testing.T) {
	one := []schemas.GPUInfo{gtx(0, gtxUUID, 4096)}
	if err := Fits(one, &schemas.GPURequest{Count: 1, MinVRAMMB: 3500}); err != nil {
		t.Errorf("3500 MiB on a 4096 MiB card: %v", err)
	}
	cases := []struct {
		devs []schemas.GPUInfo
		req  schemas.GPURequest
		want string
	}{
		{nil, schemas.GPURequest{Count: 1}, "no GPU the bench can use"},
		{one, schemas.GPURequest{Count: 1, MinVRAMMB: 8000}, "GPU 0 NVIDIA GeForce GTX 1650 has 4096 MiB"},
		{one, schemas.GPURequest{Count: 2}, "has 1 GPU(s) the bench can use"},
	}
	for _, c := range cases {
		err := Fits(c.devs, &c.req)
		if err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("Fits(%v) = %v, want %q", c.req, err, c.want)
		}
	}
}

func TestPoolOneRunPerGPU(t *testing.T) {
	p, err := NewPool("", []schemas.GPUInfo{gtx(0, gtxUUID, 4096)}, 0, "")
	if err != nil {
		t.Fatal(err)
	}
	if p.Mode() != schemas.GPUModeCDI {
		t.Errorf("default mode %q", p.Mode())
	}
	a, release, err := p.Acquire(context.Background(), &schemas.GPURequest{Count: 1})
	if err != nil || len(a.GPUs) != 1 || a.SampleMS != DefaultSampleMS {
		t.Fatalf("first acquire: %+v %v", a, err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	if _, _, err := p.Acquire(ctx, &schemas.GPURequest{Count: 1}); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("second acquire while held: %v, want a wait that ends with the context", err)
	}
	got := make(chan error, 1)
	go func() {
		_, r2, err := p.Acquire(context.Background(), &schemas.GPURequest{Count: 1})
		if err == nil {
			r2()
		}
		got <- err
	}()
	time.Sleep(20 * time.Millisecond)
	if p.Busy() != 1 {
		t.Errorf("busy %d while held", p.Busy())
	}
	release()
	release() // safe twice
	select {
	case err := <-got:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("a released GPU did not wake the waiting run")
	}
	if _, _, err := p.Acquire(context.Background(), &schemas.GPURequest{Count: 1, MinVRAMMB: 9000}); err == nil {
		t.Error("a request the machine can never meet must fail at once, not wait")
	}
}

func TestNewPoolRefuses(t *testing.T) {
	if _, err := NewPool("privileged", nil, 0, ""); err == nil {
		t.Error("unknown mode accepted")
	}
	if _, err := NewPool("cdi", nil, 50, ""); err == nil {
		t.Error("50 ms sampler accepted")
	}
	p, _ := NewPool("cdi", []schemas.GPUInfo{gtx(0, gtxUUID, 4096)}, -1, "")
	a, release, _ := p.Acquire(context.Background(), &schemas.GPURequest{Count: 1})
	defer release()
	if a.SampleMS != 0 {
		t.Errorf("negative sample_ms switches the sampler off, got %d", a.SampleMS)
	}
}

func TestContainerArgs(t *testing.T) {
	a := &schemas.GPUAssignment{Mode: schemas.GPUModeCDI, GPUs: []schemas.GPUInfo{gtx(0, "GPU-a", 1), gtx(1, "GPU-b", 1)}}
	got, err := ContainerArgs(a)
	if err != nil || !reflect.DeepEqual(got, []string{"--device", "nvidia.com/gpu=GPU-a", "--device", "nvidia.com/gpu=GPU-b"}) {
		t.Errorf("cdi: %v %v", got, err)
	}
	a.Mode = schemas.GPUModeDeviceRequests
	got, _ = ContainerArgs(a)
	if !reflect.DeepEqual(got, []string{"--gpus", `"device=GPU-a,GPU-b"`}) {
		t.Errorf("device requests: %v", got)
	}
	a.GPUs = a.GPUs[:1]
	got, _ = ContainerArgs(a)
	if !reflect.DeepEqual(got, []string{"--gpus", "device=GPU-a"}) {
		t.Errorf("one device request: %v", got)
	}
	for _, arg := range got {
		if strings.Contains(arg, "privileged") || strings.Contains(arg, "cap-add") {
			t.Errorf("GPU args must never ask for privileges: %v", got)
		}
	}
	if got, err := ContainerArgs(nil); got != nil || err != nil {
		t.Errorf("nil assignment: %v %v", got, err)
	}
	bad := &schemas.GPUAssignment{Mode: schemas.GPUModeCDI, GPUs: []schemas.GPUInfo{{UUID: "GPU-a,all"}}}
	if _, err := ContainerArgs(bad); err == nil {
		t.Error("a uuid that could smuggle a second device was accepted")
	}
	h := Hardware(&schemas.GPUAssignment{Mode: schemas.GPUModeCDI, GPUs: []schemas.GPUInfo{gtx(0, "GPU-a", 4096)}})
	if h.Mode != "cdi" || len(h.Devices) != 1 || h.Devices[0] != "nvidia.com/gpu=GPU-a" {
		t.Errorf("hardware record %+v", h)
	}
}

func TestParseSampleAndListed(t *testing.T) {
	s, err := ParseSample(gtxUUID + ", 37, 1234, 4096, 12.34, 61")
	if err != nil || s.UtilPct != 37 || s.MemUsedMB != 1234 || *s.PowerW != 12.34 || *s.TempC != 61 {
		t.Fatalf("ParseSample: %+v %v", s, err)
	}
	s, err = ParseSample(gtxUUID + ", 0, 52, 4096, [N/A], [Not Supported]")
	if err != nil || s.PowerW != nil || s.TempC != nil {
		t.Fatalf("N/A fields: %+v %v", s, err)
	}
	if _, err := ParseSample(gtxUUID + ", [N/A], 52, 4096, 1, 1"); err == nil {
		t.Error("utilization N/A accepted")
	}
	if got := ListedUUIDs(listText); len(got) != 1 || got[0] != gtxUUID {
		t.Errorf("ListedUUIDs = %v", got)
	}
}

func TestMerge(t *testing.T) {
	a := &schemas.GPUSamplerSummary{Samples: 2, AgentSeconds: 1, GPUSeconds: 1, PeakVRAMMB: 100, MeanUtilPct: 50, Sessions: 1}
	b := &schemas.GPUSamplerSummary{Samples: 6, AgentSeconds: 3, GPUSeconds: 3, PeakVRAMMB: 300, MeanUtilPct: 10, Sessions: 1, Error: "x"}
	m := Merge(Merge(nil, a), b)
	if m.Samples != 8 || m.AgentSeconds != 4 || m.PeakVRAMMB != 300 || m.MeanUtilPct != 20 || m.Sessions != 2 || !strings.Contains(m.Error, "session 2: x") {
		t.Errorf("Merge = %+v", m)
	}
	if a.Samples != 2 {
		t.Error("Merge changed its input")
	}
}

func TestSamplerOffAndNil(t *testing.T) {
	if s := StartSampler(context.Background(), nil, t.TempDir(), 1); s != nil {
		t.Error("no assignment must give no sampler")
	}
	if s := StartSampler(context.Background(), &schemas.GPUAssignment{GPUs: []schemas.GPUInfo{gtx(0, "GPU-a", 1)}}, t.TempDir(), 1); s != nil {
		t.Error("sample_ms 0 must give no sampler")
	}
	var s *Sampler
	if s.Stop() != nil {
		t.Error("Stop on nil sampler")
	}
	missing := &schemas.GPUAssignment{GPUs: []schemas.GPUInfo{gtx(0, "GPU-a", 1)}, SampleMS: 100, SMI: "/nonexistent/nvidia-smi"}
	sum := StartSampler(context.Background(), missing, t.TempDir(), 1).Stop()
	if sum == nil || sum.Error == "" || sum.Samples != 0 {
		t.Errorf("missing nvidia-smi must be reported, not zero: %+v", sum)
	}
}

func TestModel(t *testing.T) {
	if Model(gtx(0, "GPU-a", 4096)) == Model(gtx(1, "GPU-b", 8192)) {
		t.Error("two memory sizes of one card name must be two models")
	}
}

// fakeSMI is a shell script standing in for nvidia-smi: one reading per
// call, or a reading every 50 ms with -lms, memory rising from 100 MiB.
const fakeSMI = `#!/bin/sh
case "$*" in
*-lms*) i=0; while :; do i=$((i+1)); echo "GPU-a, 50, $((100 + i * 10)), 4096, 12.5, 60"; sleep 0.05; done ;;
*) echo "GPU-a, 0, 100, 4096, [N/A], 55" ;;
esac
`

func TestSamplerWithFakeSMI(t *testing.T) {
	dir := t.TempDir()
	smi := dir + "/nvidia-smi"
	if err := writeExec(smi, fakeSMI); err != nil {
		t.Fatal(err)
	}
	a := &schemas.GPUAssignment{Mode: "cdi", GPUs: []schemas.GPUInfo{gtx(0, "GPU-a", 4096)}, SampleMS: 100, SMI: smi}
	s := StartSampler(context.Background(), a, dir, 1)
	time.Sleep(400 * time.Millisecond)
	sum := s.Stop()
	if sum.Samples < 3 || sum.BaselineMB != 100 || sum.PeakVRAMMB < 20 || sum.MeanUtilPct != 50 || sum.Error != "" || sum.GPUSeconds <= 0 {
		t.Fatalf("summary %+v", sum)
	}
	lines, err := readLines(dir + "/" + SamplesFile)
	if err != nil || len(lines) != sum.Samples+1 || !strings.Contains(lines[0], `"phase":"baseline"`) || !strings.Contains(lines[1], `"phase":"agent"`) {
		t.Fatalf("samples file: %d lines, %v", len(lines), err)
	}
}

func writeExec(path, body string) error {
	return os.WriteFile(path, []byte(body), 0o755)
}

func readLines(path string) ([]string, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return strings.Split(strings.TrimSpace(string(b)), "\n"), nil
}

// TestPoolAcquireDevices is QC2-10-09: a lease's named devices are taken
// exactly, the big device is not given to a small lease's first fit, and a
// held named device is waited for.
func TestPoolAcquireDevices(t *testing.T) {
	big, small := gtx(0, "GPU-big", 24576), gtx(1, "GPU-small", 8192)
	p, err := NewPool("", []schemas.GPUInfo{big, small}, 0, "")
	if err != nil {
		t.Fatal(err)
	}
	a, release, err := p.AcquireDevices(context.Background(), []string{"GPU-small"})
	if err != nil || len(a.GPUs) != 1 || a.GPUs[0].UUID != "GPU-small" {
		t.Fatalf("named acquire: %+v %v", a, err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	if _, _, err := p.AcquireDevices(ctx, []string{"GPU-small"}); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("held device: %v, want a wait", err)
	}
	b, r2, err := p.AcquireDevices(context.Background(), []string{"GPU-big"})
	if err != nil || b.GPUs[0].UUID != "GPU-big" {
		t.Fatalf("big device: %+v %v", b, err)
	}
	r2()
	release()
	if _, _, err := p.AcquireDevices(context.Background(), []string{"GPU-none"}); err == nil {
		t.Fatal("an unknown device was accepted")
	}
}
