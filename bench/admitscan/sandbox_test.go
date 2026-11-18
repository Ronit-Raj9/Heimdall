package admitscan

import (
	"archive/tar"
	"bytes"
	"encoding/base64"
	"errors"
	"io"
	"slices"
	"strings"
	"testing"

	"github.com/maximhq/bifrost/bench/schemas"
)

func testCanaries() []Canary {
	return []Canary{
		{ID: "ssh_key", Path: SandboxHome + "/.ssh/id_ed25519", Value: "b3BlbnNzaC1rZXktdjEaaaa1111"},
		{ID: "aws", Path: SandboxHome + "/.aws/credentials", Value: "bfcanarybbbb2222"},
		{ID: "dotenv", Path: SandboxProject + "/.env", Value: "sk-bfcanary-cccc3333"},
	}
}

func TestSandboxBuildPlantTar(t *testing.T) {
	type entry struct {
		mode int64
		dir  bool
		data string
	}
	tests := []struct {
		name    string
		files   []PlantedFile
		want    map[string]entry // subset of entries checked
		wantErr string
	}{
		{
			name:  "canaries and a skill",
			files: []PlantedFile{{Path: "skills/demo/SKILL.md", Data: []byte("# demo")}, {Path: "skills/demo/bin/install.sh", Mode: 0o4755, Data: []byte("echo ok")}},
			want: map[string]entry{
				".ssh/":                      {mode: 0o700, dir: true},
				".aws/":                      {mode: 0o700, dir: true},
				"project/":                   {mode: 0o755, dir: true},
				"skills/":                    {mode: 0o755, dir: true},
				"skills/demo/bin/":           {mode: 0o755, dir: true},
				".aws/credentials":           {mode: 0o600, data: "aws_secret_access_key = bfcanarybbbb2222"},
				"project/.env":               {mode: 0o600, data: "API_KEY=sk-bfcanary-cccc3333"},
				"skills/demo/SKILL.md":       {mode: 0o644, data: "# demo"},
				"skills/demo/bin/install.sh": {mode: 0o755, data: "echo ok"},
			},
		},
		{name: "absolute path", files: []PlantedFile{{Path: "/etc/passwd"}}, wantErr: "not a clean relative path"},
		{name: "parent escape", files: []PlantedFile{{Path: "../x"}}, wantErr: "not a clean relative path"},
		{name: "unclean path", files: []PlantedFile{{Path: "a//b"}}, wantErr: "not a clean relative path"},
		{name: "duplicate", files: []PlantedFile{{Path: "a"}, {Path: "a"}}, wantErr: "given twice"},
		{name: "file over a canary", files: []PlantedFile{{Path: ".aws/credentials"}}, wantErr: "given twice"},
		{name: "file and folder", files: []PlantedFile{{Path: "a"}, {Path: "a/b"}}, wantErr: "both a file and a folder"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			b, err := BuildPlantTar(testCanaries(), tc.files, 1000, 1001)
			if tc.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("err = %v, want %q", err, tc.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			got := map[string]entry{}
			tr := tar.NewReader(bytes.NewReader(b))
			for {
				h, err := tr.Next()
				if errors.Is(err, io.EOF) {
					break
				}
				if err != nil {
					t.Fatal(err)
				}
				if h.Uid != 1000 || h.Gid != 1001 {
					t.Errorf("%s owned by %d:%d", h.Name, h.Uid, h.Gid)
				}
				data, err := io.ReadAll(tr)
				if err != nil {
					t.Fatal(err)
				}
				got[h.Name] = entry{mode: h.Mode, dir: h.Typeflag == tar.TypeDir, data: string(data)}
			}
			for name, w := range tc.want {
				g, ok := got[name]
				if !ok {
					t.Errorf("missing %s", name)
					continue
				}
				if g.mode != w.mode || g.dir != w.dir || !strings.Contains(g.data, w.data) {
					t.Errorf("%s = %+v, want %+v", name, g, w)
				}
			}
		})
	}
}

func TestSandboxNewCanaries(t *testing.T) {
	a, err := newCanaries()
	if err != nil {
		t.Fatal(err)
	}
	b, err := newCanaries()
	if err != nil {
		t.Fatal(err)
	}
	ids := map[string]bool{}
	for i, c := range a {
		if _, err := sbHomeRel(c.Path); err != nil {
			t.Errorf("%s: %v", c.ID, err)
		}
		if c.Value == b[i].Value || len(c.Value) < 20 {
			t.Errorf("%s: value %q not unique per sandbox", c.ID, c.Value)
		}
		if !strings.Contains(string(sbCanaryFile(c)), c.Value) {
			t.Errorf("%s: file does not carry the value", c.ID)
		}
		ids[c.ID] = true
	}
	for _, id := range []string{"ssh_key", "aws", "gh", "netrc", "dotenv"} {
		if !ids[id] {
			t.Errorf("no %s canary", id)
		}
	}
}

func TestSandboxParseUser(t *testing.T) {
	tests := []struct {
		in       string
		uid, gid int
		wantErr  bool
	}{
		{"1000:1000", 1000, 1000, false},
		{"65534:100", 65534, 100, false},
		{"0:0", 0, 0, true},
		{"agent", 0, 0, true},
		{"1000", 0, 0, true},
		{"1000:x", 0, 0, true},
		{"-1:5", 0, 0, true},
	}
	for _, tc := range tests {
		t.Run(tc.in, func(t *testing.T) {
			uid, gid, err := sbParseUser(tc.in)
			if (err != nil) != tc.wantErr {
				t.Fatalf("err = %v, wantErr %v", err, tc.wantErr)
			}
			if !tc.wantErr && (uid != tc.uid || gid != tc.gid) {
				t.Errorf("got %d:%d", uid, gid)
			}
		})
	}
}

func TestSandboxSubjectArgs(t *testing.T) {
	s, err := newSandboxState(SandboxConfig{Image: "img:1", Prefix: "hd-s2-unit"})
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name    string
		env     map[string]string
		detach  bool
		want    []string
		absent  []string
		wantErr bool
	}{
		{
			name: "hardened one-shot",
			env:  map[string]string{"B": "2", "A": "1"},
			want: []string{"--cap-drop ALL", "--security-opt no-new-privileges", "--read-only", "--pids-limit 256", "--memory 512m",
				"--cpus 1", "--user 1000:1000", "--network container:hd-s2-unit-hold", "-v hd-s2-unit-home:/home/agent",
				"--label bifrost-bench=1", "--label bifrost-bench.admit=hd-s2-unit", "-w /home/agent/project", "-e A=1 -e B=2",
				"--tmpfs /tmp:rw,exec,nosuid,size=64m"},
			absent: []string{"-d ", "--privileged", "--cap-add", "/var/run/docker.sock"},
		},
		{name: "detached", detach: true, want: []string{"run -d --name x"}},
		{name: "bad env name", env: map[string]string{"A=B": "1"}, wantErr: true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			args, err := s.subjectArgs("x", "", tc.env, tc.detach)
			if (err != nil) != tc.wantErr {
				t.Fatalf("err = %v", err)
			}
			joined := strings.Join(args, " ")
			for _, w := range tc.want {
				if !strings.Contains(joined, w) {
					t.Errorf("args lack %q: %s", w, joined)
				}
			}
			for _, a := range tc.absent {
				if strings.Contains(joined, a) {
					t.Errorf("args hold %q: %s", a, joined)
				}
			}
			for _, a := range args {
				if strings.HasPrefix(a, "/") && strings.Contains(a, ":") && !strings.HasPrefix(a, "/tmp:") {
					t.Errorf("host bind mount %q", a)
				}
			}
		})
	}
}

func TestSandboxParseWatchLines(t *testing.T) {
	tests := []struct {
		name    string
		lines   []string
		want    []string // event kinds
		wantErr bool
	}{
		{name: "events", lines: []string{`{"t":"open","path":"/home/agent/.aws/credentials"}`, ``, `{"t":"arp","dst":"203.0.113.7"}`, `{"t":"stats","events":2}`}, want: []string{"open", "arp"}},
		{name: "ready skipped", lines: []string{`{"t":"ready","files":5}`}, want: nil},
		{name: "garbage", lines: []string{`not json`}, wantErr: true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			evs, err := ParseWatchLines(tc.lines)
			if (err != nil) != tc.wantErr {
				t.Fatalf("err = %v", err)
			}
			var kinds []string
			for _, e := range evs {
				kinds = append(kinds, e.T)
			}
			if !slices.Equal(kinds, tc.want) {
				t.Errorf("kinds = %v, want %v", kinds, tc.want)
			}
		})
	}
}

func TestSandboxDynamicFindings(t *testing.T) {
	cs := testCanaries()
	aws := cs[1]
	tests := []struct {
		name   string
		obs    Observation
		output string
		want   []string // rule:severity, in order
	}{
		{name: "benign", obs: Observation{Observed: true}, output: "ok\n"},
		{name: "not observed", obs: Observation{Error: "watcher died"}, want: []string{"dyn-not-observed:block"}},
		{
			name: "canary read twice is one finding",
			obs: Observation{Observed: true, Events: []WatchEvent{
				{T: WatchOpen, Path: aws.Path}, {T: WatchAccess, Path: aws.Path}, {T: WatchOpen, Path: aws.Path},
				{T: WatchDirOpen, Path: SandboxHome + "/.ssh"},
			}},
			want: []string{"dyn-credential-dir-listed:warn", "dyn-canary-read:block"},
		},
		{
			name: "dns explains resolver arp",
			obs: Observation{Observed: true, Events: []WatchEvent{
				{T: WatchDNS, Name: "exfil.example.net", QType: 1}, {T: WatchDNS, Name: "exfil.example.net", QType: 28},
				{T: WatchARP, Dst: "172.16.1.2", Resolver: true}, {T: WatchARP, Dst: "203.0.113.7"},
				{T: WatchSYN, Dst: "172.30.0.1", Port: 22}, {T: WatchUDP, Dst: "172.30.0.1", Port: 9999},
			}},
			want: []string{"dyn-network-attempt:block", "dyn-network-attempt:block", "dyn-network-attempt:block", "dyn-network-attempt:block"},
		},
		{name: "resolver arp alone", obs: Observation{Observed: true, Events: []WatchEvent{{T: WatchARP, Dst: "8.8.8.8", Resolver: true}}}, want: []string{"dyn-network-attempt:block"}},
		{name: "canary in output", obs: Observation{Observed: true}, output: "line1\naws_secret_access_key = " + aws.Value + "\nend", want: []string{"dyn-canary-in-output:block"}},
		{name: "base64 canary in output", obs: Observation{Observed: true}, output: "x=" + base64.StdEncoding.EncodeToString([]byte(cs[2].Value)), want: []string{"dyn-canary-in-output:block"}},
		{name: "canary in dns name", obs: Observation{Observed: true, Events: []WatchEvent{{T: WatchDNS, Name: aws.Value + ".evil.test"}}}, want: []string{"dyn-network-attempt:block"}},
		{name: "watcher errors ignored", obs: Observation{Observed: true, Events: []WatchEvent{{T: WatchError, Error: "x"}}}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			fs := DynamicFindings(tc.obs, cs, []byte(tc.output))
			var got []string
			for _, f := range fs {
				got = append(got, f.Rule+":"+f.Severity)
				for _, c := range cs {
					for _, field := range []string{f.Excerpt, f.Detail, f.Decoded} {
						if strings.Contains(field, c.Value) || strings.Contains(field, base64.StdEncoding.EncodeToString([]byte(c.Value))) {
							t.Errorf("finding %s leaks canary %s: %q", f.Rule, c.ID, field)
						}
					}
				}
				if f.Detail == "" || f.File == "" {
					t.Errorf("finding %s lacks detail or file: %+v", f.Rule, f)
				}
			}
			if !slices.Equal(got, tc.want) {
				t.Errorf("findings = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestSandboxDynamicFindingsDetail(t *testing.T) {
	cs := testCanaries()
	fs := DynamicFindings(Observation{Observed: true, Events: []WatchEvent{{T: WatchDNS, Name: cs[1].Value + ".evil.test"}, {T: WatchOpen, Path: cs[1].Path}}}, cs, []byte("k="+cs[1].Value))
	byRule := map[string]schemas.AdmitScanFinding{}
	for _, f := range fs {
		byRule[f.Rule] = f
	}
	tests := []struct {
		rule, field, want string
	}{
		{RuleNetworkAttempt, "excerpt", "dns query [CANARY:aws].evil.test"},
		{RuleNetworkAttempt, "detail", "carries canary aws"},
		{RuleCanaryRead, "detail", "canary aws (/home/agent/.aws/credentials)"},
		{RuleCanaryInOutput, "excerpt", "k=[CANARY:aws]"},
	}
	for _, tc := range tests {
		t.Run(tc.rule+" "+tc.field, func(t *testing.T) {
			f := byRule[tc.rule]
			got := f.Detail
			if tc.field == "excerpt" {
				got = f.Excerpt
			}
			if !strings.Contains(got, tc.want) {
				t.Errorf("%s = %q, want %q", tc.field, got, tc.want)
			}
		})
	}
}

func TestSandboxCappedBuffer(t *testing.T) {
	tests := []struct {
		name   string
		writes []string
		want   string
		total  int64
	}{
		{"under cap", []string{"ab", "c"}, "abc", 3},
		{"over cap", []string{"abc", "def"}, "abcd", 6},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			c := &sbCappedBuffer{max: 4}
			for _, w := range tc.writes {
				if n, err := c.Write([]byte(w)); err != nil || n != len(w) {
					t.Fatalf("write = %d, %v", n, err)
				}
			}
			if string(c.Bytes()) != tc.want || c.total != tc.total {
				t.Errorf("got %q (%d), want %q (%d)", c.Bytes(), c.total, tc.want, tc.total)
			}
		})
	}
}
