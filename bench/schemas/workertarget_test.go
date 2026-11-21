package schemas

import "testing"

func TestParseWorkerTarget(t *testing.T) {
	for in, want := range map[string][2]string{
		"": {"shared", ""}, "shared": {"shared", ""}, "default": {"shared", ""},
		"platform:modal": {"platform", "modal"}, "worker:gpu-box": {"worker", "gpu-box"},
	} {
		k, n, err := ParseWorkerTarget(in)
		if err != nil || k != want[0] || n != want[1] {
			t.Errorf("ParseWorkerTarget(%q) = %q %q %v", in, k, n, err)
		}
		if got := WorkerTargetOf(k, n); in != "" && in != "default" && got != in {
			t.Errorf("WorkerTargetOf(%q, %q) = %q", k, n, got)
		}
	}
	for _, bad := range []string{"platform:", "vm:x", "modal", "worker"} {
		if _, _, err := ParseWorkerTarget(bad); err == nil {
			t.Errorf("ParseWorkerTarget(%q) accepted", bad)
		}
	}
}
