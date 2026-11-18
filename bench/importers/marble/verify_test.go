package marble

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// verifyCase runs assets/verify.py (python3 -B -I, as the suite does) on a
// diagnosis and an optional mailbox record, and returns its reward file.
type rewardFile struct {
	Reward     float64            `json:"reward"`
	Dimensions map[string]float64 `json:"dimensions"`
}

func needPython(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("python3"); err != nil {
		t.Skip("python3 not on PATH")
	}
}

// chainExpected is a task with a chain graph agent1 - agent2 - agent3, two
// true root causes, and a label holding a comma, as upstream has one.
func chainExpected() map[string]any {
	return map[string]any{
		"scenario": "database", "task_id": 2,
		"labels":      []string{"LOCK_CONTENTION", "VACUUM", "POOR_JOIN_PERFORMANCE,CPU_CONTENTION", "FETCH_LARGE_DATA"},
		"root_causes": []string{"VACUUM", "LOCK_CONTENTION"}, "max_labels": 3,
		"artifact_id": artifactID, "artifact_name": artifactName,
		"routes": map[string]any{"members": []string{"agent1", "agent2", "agent3"}, "lead": "agent1",
			"allowed": map[string][]string{"agent1": {"agent2"}, "agent2": {"agent1", "agent3"}, "agent3": {"agent2"}}},
	}
}

func runVerify(t *testing.T, expected map[string]any, diagnosis *string, messages []map[string]any) (rewardFile, string) {
	t.Helper()
	dir := t.TempDir()
	tests, arts, state, out := filepath.Join(dir, "tests"), filepath.Join(dir, "artifacts"), filepath.Join(dir, "state"), filepath.Join(dir, "out")
	for _, d := range []string{tests, filepath.Join(arts, artifactID), filepath.Join(state, "mailbox")} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	b, _ := json.Marshal(expected)
	if err := os.WriteFile(filepath.Join(tests, "expected.json"), b, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(tests, "verify.py"), []byte(verifyScript), 0o644); err != nil {
		t.Fatal(err)
	}
	if diagnosis != nil {
		if err := os.WriteFile(filepath.Join(arts, artifactID, artifactName), []byte(*diagnosis), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if messages != nil {
		var lines []string
		for _, m := range messages {
			b, _ := json.Marshal(m)
			lines = append(lines, string(b))
		}
		if err := os.WriteFile(filepath.Join(state, "mailbox", "messages.jsonl"), []byte(strings.Join(lines, "\n")+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	cmd := exec.Command("python3", "-B", "-I", filepath.Join(tests, "verify.py"))
	cmd.Env = append(os.Environ(), "MARBLE_TESTS="+tests, "MARBLE_ARTIFACTS="+arts, "MARBLE_STATE="+state, "MARBLE_OUT="+out)
	stdout, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("verify.py: %v\n%s", err, stdout)
	}
	var rf rewardFile
	rb, err := os.ReadFile(filepath.Join(out, "reward.json"))
	if err != nil || json.Unmarshal(rb, &rf) != nil {
		t.Fatalf("reward.json: %v %s", err, rb)
	}
	return rf, string(stdout)
}

func str(s string) *string { return &s }

func TestVerifyScoring(t *testing.T) {
	needPython(t)
	for _, tc := range []struct {
		name      string
		diagnosis *string
		reward    float64
		say       string
		dims      map[string]float64
	}{
		{"both true causes", str("# Diagnosis\n\nROOT_CAUSES: VACUUM, LOCK_CONTENTION\n"), 1, "every true root cause named", map[string]float64{"accuracy": 1, "labels_named": 2, "over_limit": 0}},
		{"three within the limit", str("ROOT_CAUSES: LOCK_CONTENTION, VACUUM, FETCH_LARGE_DATA"), 1, "", map[string]float64{"labels_named": 3}},
		{"markdown and lower case", str("**root_causes:** `vacuum`, `lock_contention`\n"), 1, "", nil},
		{"the last line counts", str("ROOT_CAUSES: FETCH_LARGE_DATA\n...\nROOT_CAUSES: VACUUM, LOCK_CONTENTION\n"), 1, "", nil},
		{"one of two", str("ROOT_CAUSES: VACUUM, FETCH_LARGE_DATA"), 0.5, "missed true root cause(s): LOCK_CONTENTION", map[string]float64{"accuracy": 0.5}},
		{"comma label is one label", str("ROOT_CAUSES: VACUUM, LOCK_CONTENTION, POOR_JOIN_PERFORMANCE,CPU_CONTENTION"), 1, "", map[string]float64{"labels_named": 3, "unknown_labels": 0}},
		{"every label is over the limit", str("ROOT_CAUSES: LOCK_CONTENTION, VACUUM, POOR_JOIN_PERFORMANCE,CPU_CONTENTION, FETCH_LARGE_DATA"), 0, "named 4 causes, the task allows at most 3", map[string]float64{"over_limit": 1, "accuracy": 1}},
		{"unknown labels count toward the limit", str("ROOT_CAUSES: VACUUM, LOCK_CONTENTION, MISSING_INDEXES, CPU_HOG"), 0, "named 4 causes", map[string]float64{"unknown_labels": 2}},
		{"no line", str("I think it is VACUUM and LOCK_CONTENTION."), 0, "no ROOT_CAUSES: line", map[string]float64{"root_cause_line": 0}},
		{"no file", nil, 0, "no diagnosis.md delivered", nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rf, out := runVerify(t, chainExpected(), tc.diagnosis, nil)
			if rf.Reward != tc.reward || !strings.Contains(out, tc.say) {
				t.Errorf("reward %v (want %v), output %q (want %q)", rf.Reward, tc.reward, out, tc.say)
			}
			for k, v := range tc.dims {
				if rf.Dimensions[k] != v {
					t.Errorf("dimension %s = %v, want %v (%v)", k, rf.Dimensions[k], v, rf.Dimensions)
				}
			}
		})
	}
}

func TestVerifyRoutes(t *testing.T) {
	needPython(t)
	good := str("ROOT_CAUSES: VACUUM, LOCK_CONTENTION\n")
	msg := func(id, from, sender, to string) map[string]any {
		return map[string]any{"id": id, "from": from, "sender": sender, "to": to, "body": "x"}
	}
	for _, tc := range []struct {
		name       string
		messages   []map[string]any
		reward     float64
		violations float64
		say        string
	}{
		{"along the chain", []map[string]any{msg("1", "agent3", "agent3", "agent2"), msg("2", "agent2", "agent2", "agent1"), msg("3", "agent1", "agent1", "agent2")}, 1, 0, ""},
		{"skipping a hop", []map[string]any{msg("1", "agent3", "agent3", "agent1")}, 0, 1, "agent3 -> agent1 (message 1)"},
		{"the proven sender wins over a spoofed from", []map[string]any{msg("1", "agent2", "agent3", "agent1")}, 0, 1, "agent3 -> agent1"},
		{"declared from when no proven sender", []map[string]any{msg("1", "agent1", "", "agent3")}, 0, 1, "agent1 -> agent3"},
		{"a broadcast reaches a non-neighbour", []map[string]any{msg("1", "agent1", "agent1", "*")}, 0, 1, "agent1 -> agent3"},
		{"a broadcast from the middle is fine", []map[string]any{msg("1", "agent2", "agent2", "*")}, 1, 0, ""},
		{"planted messages are not the team's", []map[string]any{{"id": "1", "from": "agent1", "to": "agent3", "planted": true, "body": "x"}}, 1, 0, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rf, out := runVerify(t, chainExpected(), good, tc.messages)
			if rf.Reward != tc.reward || rf.Dimensions["route_violations"] != tc.violations || !strings.Contains(out, tc.say) {
				t.Errorf("reward %v violations %v output %q; want %v %v %q", rf.Reward, rf.Dimensions["route_violations"], out, tc.reward, tc.violations, tc.say)
			}
		})
	}
}
