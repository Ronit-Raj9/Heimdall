package k8s

import (
	"archive/tar"
	"bytes"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/maximhq/bifrost/bench/environments"
	"github.com/maximhq/bifrost/bench/schemas"
)

func testInput() podInput {
	return podInput{RunID: "r_abc", ExpID: "e_x", AgentImage: "agent:1", HelperImage: "helper:1", PullPolicy: "IfNotPresent",
		Argv: []string{"/bin/sh", "-c", "echo hi"}, Workdir: "/workspace", UID: 1000, GID: 1000, CPUs: 1.5, MemGB: 1, WallS: 900,
		ProcWatch: true, FSWatch: true, IntervalMS: 20, GatewayIP: "10.96.0.50", GatewayHost: "bifrost", Collector: true, CollectorImage: "bench/bifrost-bench:dev"}
}

func TestPodSecurityBaseline(t *testing.T) {
	p := buildPod(testInput(), nil)
	s := p.Spec
	if s.HostNetwork || s.HostPID || s.HostIPC {
		t.Fatal("host namespaces")
	}
	if s.ShareProcessNamespace == nil || !*s.ShareProcessNamespace {
		t.Fatal("the process observer needs the shared PID namespace")
	}
	if s.AutomountServiceAccountToken == nil || *s.AutomountServiceAccountToken {
		t.Fatal("the service account token must not be mounted")
	}
	if s.EnableServiceLinks == nil || *s.EnableServiceLinks {
		t.Fatal("service links leak cluster addresses into the environment")
	}
	if s.DNSPolicy != "None" || s.DNSConfig == nil || len(s.DNSConfig.Nameservers) != 1 || s.DNSConfig.Nameservers[0] != "127.0.0.1" {
		t.Fatalf("the pod gets no DNS server: %+v %+v", s.DNSPolicy, s.DNSConfig)
	}
	if s.RestartPolicy != "Never" || s.ActiveDeadlineSeconds == nil || *s.ActiveDeadlineSeconds != 900+activeDeadlineMargin {
		t.Fatal("restart policy or deadline")
	}
	raw, _ := json.Marshal(p)
	for _, bad := range []string{"hostPath", `"privileged":true`, "allowPrivilegeEscalation\":true"} {
		if bytes.Contains(raw, []byte(bad)) {
			t.Fatalf("pod spec holds %s", bad)
		}
	}
	all := append(append([]Container(nil), s.InitContainers...), s.Containers...)
	for _, c := range all {
		sc := c.SecurityContext
		if sc == nil || sc.Capabilities == nil || len(sc.Capabilities.Drop) != 1 || sc.Capabilities.Drop[0] != "ALL" {
			t.Fatalf("%s does not drop every capability", c.Name)
		}
		if sc.AllowPrivilegeEscalation == nil || *sc.AllowPrivilegeEscalation {
			t.Fatalf("%s may escalate", c.Name)
		}
		if sc.ReadOnlyRootFilesystem == nil || !*sc.ReadOnlyRootFilesystem {
			t.Fatalf("%s has a writable root", c.Name)
		}
	}
	agent := s.Containers[0]
	if agent.Name != ctrAgent || agent.SecurityContext.RunAsUser == nil || *agent.SecurityContext.RunAsUser != 1000 ||
		agent.SecurityContext.RunAsNonRoot == nil || !*agent.SecurityContext.RunAsNonRoot || len(agent.SecurityContext.Capabilities.Add) != 0 {
		t.Fatalf("agent security: %+v", agent.SecurityContext)
	}
	if !agent.Stdin || !agent.StdinOnce || agent.Command[2] != gateScript || strings.Join(agent.Command[4:], " ") != "/bin/sh -c echo hi" {
		t.Fatalf("agent is not gated: %v", agent.Command)
	}
	if agent.Resources.Limits["cpu"] != "1.5" || agent.Resources.Limits["memory"] != "1073741824" || agent.Resources.Requests["cpu"] != "1.5" {
		t.Fatalf("resources: %+v", agent.Resources)
	}
	for _, m := range agent.VolumeMounts {
		if m.Name == volObs || m.Name == volCtl || m.Name == volScripts {
			t.Fatalf("the agent must not mount %s", m.Name)
		}
	}
	if len(agent.EnvFrom) != 1 || agent.EnvFrom[0].SecretRef.Name != envSecret || len(agent.Env) != 0 {
		t.Fatal("the agent's environment must come from its secret only")
	}
}

func TestPodContainerOrderAndCapabilities(t *testing.T) {
	s := buildPod(testInput(), nil).Spec
	var names []string
	for _, c := range s.InitContainers {
		names = append(names, c.Name+":"+c.RestartPolicy)
	}
	if strings.Join(names, ",") != "prep:,fswatch:Always,procwatch:Always,collector:Always" {
		t.Fatalf("init order: %v", names)
	}
	caps := map[string]string{}
	for _, c := range append(append([]Container(nil), s.InitContainers...), s.Containers...) {
		caps[c.Name] = strings.Join(c.SecurityContext.Capabilities.Add, ",")
	}
	want := map[string]string{ctrPrep: "CHOWN,DAC_OVERRIDE,FOWNER", ctrFSWatch: "DAC_OVERRIDE", ctrProcWatch: "SYS_PTRACE", ctrCollector: "", ctrAgent: "", ctrBench: "DAC_OVERRIDE,KILL"}
	for k, v := range want {
		if caps[k] != v {
			t.Errorf("%s caps %q, want %q", k, caps[k], v)
		}
	}
	for _, c := range s.InitContainers[1:] {
		if c.StartupProbe == nil {
			t.Fatalf("%s has no startup probe, so the agent could start before it is ready", c.Name)
		}
	}
	for _, m := range s.InitContainers[1].VolumeMounts {
		if m.Name == volWorkspace && !m.ReadOnly {
			t.Fatal("the workspace observer must mount the workspace read-only")
		}
	}
	for _, m := range s.Containers[1].VolumeMounts {
		if m.Name == volWorkspace && !m.ReadOnly {
			t.Fatal("the bench helper must mount the workspace read-only")
		}
	}
	if len(s.HostAliases) != 2 || s.HostAliases[0].IP != "10.96.0.50" || s.HostAliases[0].Hostnames[0] != "bifrost" ||
		s.HostAliases[1].IP != "127.0.0.1" || s.HostAliases[1].Hostnames[0] != "toolproxy" {
		t.Fatalf("gateway host alias: %+v", s.HostAliases)
	}
}

func TestPodWithoutNetworkOrObservers(t *testing.T) {
	in := testInput()
	in.GatewayIP, in.ProcWatch, in.FSWatch, in.CPUs, in.MemGB, in.Collector = "", false, false, 0, 0, false
	s := buildPod(in, nil).Spec
	if len(s.HostAliases) != 0 {
		t.Fatal("no network, no gateway entry")
	}
	if len(s.InitContainers) != 1 {
		t.Fatalf("observers off: %d init containers", len(s.InitContainers))
	}
	if s.Containers[0].Resources != nil {
		t.Fatal("no budget, no resources block")
	}
}

func TestObserverCommandKeepsArgvBare(t *testing.T) {
	cmd := observerCommand(ctrProcWatch, "20")
	if !strings.Contains(cmd[2], "exec python3 -u - 20 < "+scriptsDir+"/procwatch.py") || !strings.HasPrefix(cmd[2], "[ -e "+obsDir+"/procwatch.jsonl ] && exec sleep") {
		t.Fatalf("observer command: %q", cmd[2])
	}
	probe := readyProbe(ctrFSWatch).Exec.Command[2]
	if !strings.Contains(probe, `"t": ?"ready"`) || !strings.Contains(probe, "fswatch.err") {
		t.Fatalf("ready probe: %q", probe)
	}
}

func TestPolicies(t *testing.T) {
	deny := denyAllPolicy(nil, nil)
	if len(deny.Spec.PolicyTypes) != 2 || len(deny.Spec.Ingress) != 0 || len(deny.Spec.Egress) != 0 || len(deny.Spec.PodSelector.MatchLabels) != 0 {
		t.Fatalf("deny-all: %+v", deny.Spec)
	}
	gw := gatewayPolicy(nil, nil, "heimdall-gw", map[string]string{"app.kubernetes.io/name": "bifrost"}, 8080)
	if len(gw.Spec.Egress) != 1 || len(gw.Spec.Egress[0].To) != 1 || gw.Spec.Egress[0].Ports[0].Port != 8080 ||
		gw.Spec.Egress[0].To[0].NamespaceSelector.MatchLabels[nsNameKey] != "heimdall-gw" || len(gw.Spec.Ingress) != 0 || gw.Spec.PolicyTypes[0] != "Egress" {
		t.Fatalf("gateway policy: %+v", gw.Spec)
	}
}

func TestNamespaceFor(t *testing.T) {
	for in, want := range map[string]string{
		"r_n2jnt4bq5o": "heimdall-run-r-n2jnt4bq5o",
		"R_ABC.def":    "heimdall-run-r-abc-def",
	} {
		if got := NamespaceFor("heimdall-run", in); got != want {
			t.Errorf("%s: %s", in, got)
		}
	}
	if got := NamespaceFor(strings.Repeat("x", 70), "r_1"); len(got) > 63 {
		t.Fatalf("too long: %d", len(got))
	}
}

func TestParseUser(t *testing.T) {
	if u, g, err := parseUser(""); err != nil || u != 1000 || g != 1000 {
		t.Fatalf("default: %d %d %v", u, g, err)
	}
	if u, g, err := parseUser("1001:50"); err != nil || u != 1001 || g != 50 {
		t.Fatalf("uid:gid: %d %d %v", u, g, err)
	}
	for _, bad := range []string{"0", "0:0", "agent", "65535", "65534", "1:x"} {
		if _, _, err := parseUser(bad); err == nil {
			t.Errorf("%q accepted", bad)
		}
	}
}

func TestRefusals(t *testing.T) {
	suite := &schemas.Suite{ID: "s"}
	base := environments.DriverSlot{Experiment: &schemas.Experiment{}, Suite: suite, Task: &schemas.Task{ID: "t"}, Agent: &schemas.AgentConfig{Adapter: "scripted"}}
	if r := refusals(base); len(r) != 0 {
		t.Fatalf("a plain diff task is refused: %v", r)
	}
	for _, layer := range []schemas.ProbeLayerName{schemas.LayerGateway, schemas.LayerFixture, schemas.LayerToolShim} {
		gw := base
		gw.Probe = &schemas.ProbeSpec{ID: "p", Layer: layer}
		if r := refusals(gw); len(r) != 0 {
			t.Fatalf("a %s probe is refused: %v", layer, r)
		}
	}
	cases := map[string]func(s *environments.DriverSlot){
		"probe layer adapter": func(s *environments.DriverSlot) { s.Probe = &schemas.ProbeSpec{Layer: schemas.LayerAdapter} },
		RefuseSessions:        func(s *environments.DriverSlot) { s.Task = &schemas.Task{Sessions: []schemas.SessionSpec{{ID: "a"}}} },
		RefuseMemory:          func(s *environments.DriverSlot) { s.Agent = &schemas.AgentConfig{Memory: &schemas.AgentMemory{}} },
		RefuseRemote:          func(s *environments.DriverSlot) { s.Agent = &schemas.AgentConfig{Adapter: "remote_http"} },
		RefuseA2A:             func(s *environments.DriverSlot) { s.Agent = &schemas.AgentConfig{Adapter: "a2a"} }, // B1
		RefuseNeutral:         func(s *environments.DriverSlot) { s.Experiment = &schemas.Experiment{Realism: schemas.RealismNeutral} },
		RefuseSnapshot: func(s *environments.DriverSlot) {
			s.Suite = &schemas.Suite{Verifier: schemas.SuiteVerifier{Mode: schemas.VerifierSnapshot}}
		},
		RefuseTaskImage: func(s *environments.DriverSlot) {
			s.Task = &schemas.Task{Environment: &schemas.TaskEnvironment{Image: "x"}}
		},
		RefuseServices: func(s *environments.DriverSlot) { s.Task = &schemas.Task{Services: []schemas.ServiceSpec{{ID: "svc"}}} },
	}
	for want, mod := range cases {
		s := base
		mod(&s)
		got := strings.Join(refusals(s), "|")
		if !strings.Contains(got, want) {
			t.Errorf("want %q, got %q", want, got)
		}
	}
}

func TestPodView(t *testing.T) {
	recs := []schemas.ProcessRecord{
		{PID: 1, PPID: 0, UID: 65535, Comm: "pause"},
		{PID: 7, PPID: 0, UID: 0, Comm: "sleep"},
		{PID: 12, PPID: 0, UID: 1000, Comm: "sh"},
		{PID: 13, PPID: 12, UID: 1000, Comm: "git"},
		{PID: 14, PPID: 13, UID: 1000, Comm: "git"},
		{PID: 20, PPID: 0, UID: 0, Comm: "grep"},
		{PID: 21, PPID: 0, UID: 65534, Comm: "bifrost-bench"},
	}
	got := podView(recs, 1000, 12)
	if len(got) != 3 || got[0].PID != 1 || got[0].PPID != 0 || got[1].PID != 13 || got[1].PPID != 1 || got[2].PPID != 13 {
		t.Fatalf("pod view: %+v", got)
	}
}

func TestEgressVerdict(t *testing.T) {
	forbidden := []probeTarget{{Name: "dns"}, {Name: "tcp:1.1.1.1:443"}}
	blocked := &EgressReport{Results: map[string]string{"dns": "blocked", "tcp:1.1.1.1:443": "blocked", "gateway": "open"}}
	if s, _ := EgressVerdict(blocked, forbidden, true); s != EgressBlocked {
		t.Fatalf("blocked: %s", s)
	}
	open := &EgressReport{Results: map[string]string{"dns": "blocked", "tcp:1.1.1.1:443": "open", "gateway": "open"}}
	if s, why := EgressVerdict(open, forbidden, true); s != EgressOpen || !strings.Contains(why, "1.1.1.1") {
		t.Fatalf("open: %s %s", s, why)
	}
	noGW := &EgressReport{Results: map[string]string{"dns": "blocked", "tcp:1.1.1.1:443": "blocked", "gateway": "blocked"}}
	if s, why := EgressVerdict(noGW, forbidden, true); s != EgressNotRun || !strings.Contains(why, "positive control") {
		t.Fatalf("a probe whose gateway is unreachable proves nothing: %s %s", s, why)
	}
	if s, _ := EgressVerdict(noGW, forbidden, false); s != EgressBlocked {
		t.Fatal("without a gateway the positive control is not required")
	}
	missing := &EgressReport{Results: map[string]string{"dns": "blocked"}}
	if s, _ := EgressVerdict(missing, forbidden, false); s != EgressNotRun {
		t.Fatal("a missing answer is not blocked")
	}
}

func TestImageMatches(t *testing.T) {
	frozen := "sha256:41c1672b2c9997c02c1f766525502a7124aa30abb3e9e84502116f4b9014e43d"
	if !imageMatches(frozen, frozen, nil) {
		t.Fatal("a loaded image reports its id")
	}
	if !imageMatches("docker.io/library/x@sha256:abc", frozen, []string{"x@sha256:abc"}) {
		t.Fatal("a pulled image reports its registry digest")
	}
	if imageMatches("sha256:other", frozen, nil) || imageMatches("", frozen, nil) || imageMatches("x@sha256:abc", frozen, []string{"x@sha256:def"}) {
		t.Fatal("a different image matched")
	}
}

func TestParseServiceURL(t *testing.T) {
	svc, ns, port, err := parseServiceURL("http://bifrost.heimdall-gw.svc.cluster.local:8080")
	if err != nil || svc != "bifrost" || ns != "heimdall-gw" || port != 8080 {
		t.Fatalf("%s %s %d %v", svc, ns, port, err)
	}
	if _, _, port, _ := parseServiceURL("http://bifrost.gw.svc"); port != 80 {
		t.Fatal("default port")
	}
	for _, bad := range []string{"http://bifrost:8080", "http://10.0.0.1:8080", "bifrost.gw.svc"} {
		if _, _, _, err := parseServiceURL(bad); err == nil {
			t.Errorf("%q accepted", bad)
		}
	}
}

func TestSeedTar(t *testing.T) {
	repo := t.TempDir()
	os.MkdirAll(filepath.Join(repo, "src", "pkg"), 0o755)
	os.WriteFile(filepath.Join(repo, "src", "pkg", "a.py"), []byte("print(1)\n"), 0o644)
	os.WriteFile(filepath.Join(repo, "run.sh"), []byte("#!/bin/sh\n"), 0o755)
	os.Symlink("run.sh", filepath.Join(repo, "link"))
	notes := filepath.Join(t.TempDir(), "notes.md")
	os.WriteFile(notes, []byte("clean notes\n"), 0o600)
	b, err := seedTar(repo, "docs/CLAUDE.md", notes)
	if err != nil {
		t.Fatal(err)
	}
	tr := tar.NewReader(bytes.NewReader(b))
	got := map[string]string{}
	for {
		h, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		body, _ := io.ReadAll(tr)
		got[h.Name] = string(h.Typeflag) + ":" + strings.TrimSpace(string(body)) + ":" + h.Linkname + ":" + strconv.FormatInt(h.Mode, 8)
	}
	want := map[string]string{
		"src/":           "5:::755",
		"src/pkg/":       "5:::755",
		"src/pkg/a.py":   "0:print(1)::644",
		"run.sh":         "0:#!/bin/sh::755",
		"link":           "2::run.sh:777",
		"docs/":          "5:::755",
		"docs/CLAUDE.md": "0:clean notes::644",
	}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("%s: got %q want %q", k, got[k], v)
		}
	}
}

func TestInotifyRow(t *testing.T) {
	old := inotifyPath
	defer func() { inotifyPath = old }()
	inotifyPath = filepath.Join(t.TempDir(), "limit")
	os.WriteFile(inotifyPath, []byte("128\n"), 0o644)
	row, ok := inotifyRow("https://127.0.0.1:6443")
	if !ok || row.Status != schemas.DoctorWarn || !strings.Contains(row.Detail, "sysctl") {
		t.Fatalf("low limit: %+v", row)
	}
	os.WriteFile(inotifyPath, []byte("8192\n"), 0o644)
	if row, _ := inotifyRow("https://localhost:6443"); row.Status != schemas.DoctorPass {
		t.Fatalf("high limit: %+v", row)
	}
	if _, ok := inotifyRow("https://k8s.example.com"); ok {
		t.Fatal("a remote cluster's host limit is not this machine's")
	}
}

func TestNodeHasImage(t *testing.T) {
	var n Node
	n.Status.Images = append(n.Status.Images, struct {
		Names     []string `json:"names"`
		SizeBytes int64    `json:"sizeBytes"`
	}{Names: []string{"docker.io/bench/coding-core-base:1", "docker.io/library/postgres:18"}})
	for _, tag := range []string{"bench/coding-core-base:1", "postgres:18"} {
		if !nodeHasImage(n, tag) {
			t.Errorf("%s not found", tag)
		}
	}
	if nodeHasImage(n, "bench/other:1") {
		t.Fatal("absent image found")
	}
}

func TestNodeImageMatches(t *testing.T) {
	var n Node
	n.Status.Images = append(n.Status.Images, struct {
		Names     []string `json:"names"`
		SizeBytes int64    `json:"sizeBytes"`
	}{Names: []string{"docker.io/bench/coding-core-base@sha256:41c1", "docker.io/bench/coding-core-base:1", "sha256:cfg9"}})
	if !nodeImageMatches(n, "sha256:cfg9", "sha256:41c1") {
		t.Fatal("a loaded image reported by its config id must match its frozen index digest")
	}
	if nodeImageMatches(n, "sha256:other", "sha256:41c1") || nodeImageMatches(n, "sha256:cfg9", "sha256:9999") {
		t.Fatal("a different image matched")
	}
}

func TestConfigFromSave(t *testing.T) {
	cfgDigest := "sha256:cfg1"
	manifest := `{"mediaType":"application/vnd.oci.image.manifest.v1+json","config":{"digest":"` + cfgDigest + `"}}`
	index := `{"manifests":[{"mediaType":"application/vnd.oci.image.manifest.v1+json","digest":"sha256:man1"}]}`
	b := makeTar(t, map[string]string{"index.json": index, "blobs/sha256/man1": manifest, "blobs/sha256/layer": "x"})
	got, err := configFromSave(bytes.NewReader(b))
	if err != nil || got != cfgDigest {
		t.Fatalf("config digest %q %v", got, err)
	}
	if _, err := configFromSave(bytes.NewReader(makeTar(t, map[string]string{"x": "y"}))); err == nil {
		t.Fatal("a tar without an index was read")
	}
}

func TestPodViewMainFirstSeenAsRoot(t *testing.T) {
	recs := []schemas.ProcessRecord{
		{PID: 1, PPID: 0, UID: 65535, Comm: "pause"},
		{PID: 131, PPID: 0, UID: 0, Comm: "runc:[2:INIT]"},
		{PID: 140, PPID: 0, UID: 0, Comm: "sleep"},
		{PID: 163, PPID: 131, UID: 1000, Comm: "sh"},
	}
	got := podView(recs, 1000, 131)
	if len(got) != 2 || got[0].PID != 1 || got[1].PID != 163 || got[1].PPID != 1 {
		t.Fatalf("pod view: %+v", got)
	}
}
