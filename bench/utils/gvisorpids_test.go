package utils

import (
	"strings"
	"testing"
)

func TestRaisePidsForGVisor(t *testing.T) {
	for _, c := range []struct {
		args    string
		runtime string
		want    string
	}{
		{"run --pids-limit 64 img", "runsc", "run --pids-limit 512 img"},
		{"run --pids-limit=8 img", "runsc", "run --pids-limit=512 img"},
		{"run --pids-limit 2048 img", "runsc", "run --pids-limit 2048 img"},
		{"run --pids-limit 64 img", "runc", "run --pids-limit 64 img"},
		{"run --pids-limit 64 img", "", "run --pids-limit 64 img"},
		{"create --runtime runsc --pids-limit 128 img", "", "create --runtime runsc --pids-limit 512 img"},
		{"create --runtime=runc --pids-limit 128 img", "runsc", "create --runtime=runc --pids-limit 128 img"},
		{"run --pids-limit -1 img", "runsc", "run --pids-limit -1 img"},
	} {
		got := strings.Join(RaisePidsForGVisor(strings.Fields(c.args), c.runtime), " ")
		if got != c.want {
			t.Errorf("%q under %q = %q, want %q", c.args, c.runtime, got, c.want)
		}
	}
}

func TestPolicyRaisesPidsUnderGVisor(t *testing.T) {
	p := &DockerPolicy{Runtime: "runsc"}
	out, err := p.Rewrite([]string{"run", "--pids-limit", "16", "img"})
	if err != nil || strings.Join(out, " ") != "run --runtime runsc --pids-limit 512 img" {
		t.Fatalf("%q, %v", out, err)
	}
	p = &DockerPolicy{Runtime: "runc"}
	out, _ = p.Rewrite([]string{"run", "--pids-limit", "16", "img"})
	if strings.Join(out, " ") != "run --runtime runc --pids-limit 16 img" {
		t.Fatalf("%q", out)
	}
}
