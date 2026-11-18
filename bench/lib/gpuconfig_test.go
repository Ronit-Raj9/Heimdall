package lib

import (
	"reflect"
	"testing"
)

// X7: the gpu section of bench.yaml (gpuconfig.go).

func TestGPUConfigValidate(t *testing.T) {
	for _, ok := range []GPUConfig{{}, {Mode: "cdi"}, {Mode: "device_requests", SampleMS: 250}, {Mode: GPUModeOff}, {SampleMS: -1}} {
		if err := ok.Validate(); err != nil {
			t.Errorf("%+v: %v", ok, err)
		}
	}
	for _, bad := range []GPUConfig{{Mode: "privileged"}, {SampleMS: 50}} {
		if err := bad.Validate(); err == nil {
			t.Errorf("%+v accepted", bad)
		}
	}
	cfg := &Config{}
	if err := applyEnv(reflect.ValueOf(cfg).Elem(), EnvPrefix, func(k string) (string, bool) {
		v, ok := map[string]string{"BENCH_GPU_MODE": "device_requests", "BENCH_GPU_DEVICES": "0,GPU-x", "BENCH_GPU_SAMPLE_MS": "250"}[k]
		return v, ok
	}); err != nil {
		t.Fatal(err)
	}
	if cfg.GPU.Mode != "device_requests" || len(cfg.GPU.Devices) != 2 || cfg.GPU.SampleMS != 250 {
		t.Errorf("environment overrides: %+v", cfg.GPU)
	}
}
