package byoworker

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestRunPodDriver(t *testing.T) {
	f := &fakeAPI{route: func(w http.ResponseWriter, r *http.Request, body map[string]any) {
		switch {
		case r.Method == "GET" && r.URL.Path == "/v1/pods":
			writeJSON(w, 200, []any{})
		case r.Method == "POST" && r.URL.Path == "/v1/pods":
			writeJSON(w, 201, map[string]any{"id": "pod_1", "desiredStatus": "RUNNING"})
		case r.Method == "GET" && r.URL.Path == "/v1/pods/pod_1":
			writeJSON(w, 200, map[string]any{"id": "pod_1", "desiredStatus": "EXITED", "lastStatusChange": "Exited by user"})
		case r.Method == "DELETE" && r.URL.Path == "/v1/pods/pod_1":
			w.WriteHeader(204)
		case r.URL.Path == "/v1/pods/gone":
			writeJSON(w, 404, map[string]any{"error": "pod not found"})
		default:
			writeJSON(w, 500, nil)
		}
	}}
	srv := httptest.NewServer(f)
	defer srv.Close()
	d := RunPod{BaseURL: srv.URL}
	settings, err := d.Validate(map[string]string{"gpu_type": "NVIDIA H100 PCIe", "gpu_count": "2", "cloud_type": "community"}, "rpa_key_123456")
	if err != nil || settings[RunPodCloudType] != "COMMUNITY" {
		t.Fatalf("validate: %v %v", settings, err)
	}
	for _, bad := range []map[string]string{{"gpu_count": "9"}, {"gpu_type": "TPU v5"}, {"cloud_type": "SPOT"}, {"disk_gb": "5"}, {"region": "us"}} {
		if _, err := d.Validate(bad, "rpa_key_123456"); !errors.Is(err, ErrInvalid) {
			t.Fatalf("validate %v: %v", bad, err)
		}
	}
	tg := Target{Settings: settings, Secret: "rpa_key_123456"}
	ctx := context.Background()
	if h := d.Check(ctx, tg); !h.OK || !strings.Contains(h.Capacity, "2 x NVIDIA H100 PCIe") || !strings.Contains(h.Capacity, "$1.99") {
		t.Fatalf("check: %+v", h)
	}
	in, err := d.Start(ctx, tg, testBoot)
	if err != nil || in.ID != "pod_1" || in.State != InstanceRunning {
		t.Fatalf("start: %+v %v", in, err)
	}
	body := f.body["POST /v1/pods"]
	env := body["env"].(map[string]any)
	if env[EnvRunnerToken] != testBoot.RunnerToken || env[EnvLabels] != "platform=runpod" {
		t.Fatalf("env: %v", env)
	}
	ids := body["gpuTypeIds"].([]any)
	if len(ids) != 1 || ids[0] != "NVIDIA H100 PCIe" || body["gpuCount"].(float64) != 2 || body["cloudType"] != "COMMUNITY" || body["imageName"] != RunPodDefaultImage {
		t.Fatalf("body: %v", body)
	}
	cmd := body["dockerStartCmd"].([]any)
	if len(cmd) != 3 || strings.Contains(cmd[2].(string), testBoot.RunnerToken) || !strings.Contains(cmd[2].(string), "--foreground") {
		t.Fatalf("start command: %v", cmd)
	}
	if st, err := d.Status(ctx, tg, "pod_1"); err != nil || st.State != InstanceStopped || !strings.Contains(st.Detail, "Exited by user") {
		t.Fatalf("status: %+v %v", st, err)
	}
	if st, err := d.Status(ctx, tg, "gone"); err != nil || st.State != InstanceStopped {
		t.Fatalf("gone: %+v %v", st, err)
	}
	if err := d.Stop(ctx, tg, "pod_1"); err != nil {
		t.Fatal(err)
	}
	if err := d.Stop(ctx, tg, "gone"); err != nil {
		t.Fatalf("stop of a gone pod: %v", err)
	}
	for _, a := range f.auth {
		if a != "Bearer rpa_key_123456" {
			t.Fatalf("auth header %q", a)
		}
	}
	if PlatformTitle(PlatformRunPod) != "RunPod" {
		t.Fatal("title")
	}
}
