package adapters

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/maximhq/bifrost/bench/utils"
)

const testChat = `{"messages":[{"role":"system","content":"You are Acme support."},{"role":"user","content":"My grinder arrived cracked."}],
"user_turns":["How long will the replacement take?"],
"attachments":[{"name":"photo.png","media_type":"image/png","path":"attachments/photo.png"},{"name":"manual.pdf","media_type":"application/pdf","url":"https://example.com/manual.pdf"}]}`

func withChat(t *testing.T, rc *schemas.RunContext, gradeOn string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(rc.Task.Dir, "chat.json"), []byte(testChat), 0o644); err != nil {
		t.Fatal(err)
	}
	rc.Task.Chat = &schemas.ChatInput{File: "chat.json", UserTurns: 1, GradeOn: gradeOn}
}

func TestChatPlans(t *testing.T) {
	rc, cfg := remoteFixture(t, remoteAgent(""))
	withChat(t, rc, "")
	plan, err := newRemoteHTTPAdapter(cfg).Plan(context.Background(), rc)
	if err != nil {
		t.Fatal(err)
	}
	if plan.Env["BENCH_REMOTE_CHAT"] != chatFile || !strings.Contains(plan.Files[chatFile], "How long will the replacement take?") {
		t.Fatalf("remote plan env %v files %v", plan.Env["BENCH_REMOTE_CHAT"], plan.Files[chatFile])
	}
	orc, ocfg := openaiFixture(t)
	withChat(t, orc, schemas.ChatGradeTranscript)
	oplan, err := newOpenAIAgentAdapter(ocfg).Plan(context.Background(), orc)
	if err != nil {
		t.Fatal(err)
	}
	var cf schemas.ChatFile
	if oplan.Env["BENCH_OPENAI_CHAT"] != chatFile || json.Unmarshal([]byte(oplan.Files[chatFile]), &cf) != nil || cf.GradeOn != "transcript" {
		t.Fatalf("openai plan env %v chat %+v", oplan.Env, cf)
	}
	// A broken chat file refuses the plan.
	if err := os.WriteFile(filepath.Join(rc.Task.Dir, "chat.json"), []byte(`{"messages":[{"role":"assistant","content":"x"}]}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := newRemoteHTTPAdapter(cfg).Plan(context.Background(), rc); err == nil || !strings.Contains(err.Error(), "user's") {
		t.Fatalf("bad chat accepted: %v", err)
	}
	if !HoldsConversation(AdapterRemoteHTTP) || !HoldsConversation(AdapterOpenAIAgent) || HoldsConversation("claude_code") {
		t.Fatal("HoldsConversation")
	}
}

// chatAgent is a deployed chat agent that records every request.
type chatAgent struct {
	mu     sync.Mutex
	bodies []map[string]any
}

func (f *chatAgent) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	raw, _ := io.ReadAll(r.Body)
	var body map[string]any
	if err := json.Unmarshal(raw, &body); err != nil {
		http.Error(w, err.Error(), 400)
		return
	}
	f.mu.Lock()
	f.bodies = append(f.bodies, body)
	n := len(f.bodies)
	f.mu.Unlock()
	w.Header().Set("Content-Type", "application/json")
	reply := "Sorry about that, a replacement is on its way."
	if n == 2 {
		reply = "About 3 days."
	}
	_ = json.NewEncoder(w).Encode(map[string]any{"answer": reply})
}

// chatWorkspace writes the chat file and the stored attachment.
func chatWorkspace(t *testing.T, gradeOn string) (dir, chat, ws string) {
	t.Helper()
	dir = t.TempDir()
	ws = filepath.Join(dir, "ws")
	if err := os.MkdirAll(filepath.Join(ws, "attachments"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(ws, "attachments", "photo.png"), []byte("\x89PNG"), 0o644); err != nil {
		t.Fatal(err)
	}
	var cf map[string]any
	_ = json.Unmarshal([]byte(testChat), &cf)
	cf["grade_on"] = gradeOn
	b, _ := json.Marshal(cf)
	chat = filepath.Join(dir, "chat.json")
	if err := os.WriteFile(chat, b, 0o644); err != nil {
		t.Fatal(err)
	}
	return dir, chat, ws
}

func TestRemoteHTTPLauncherConversation(t *testing.T) {
	py, err := exec.LookPath("python3")
	if err != nil {
		t.Skip("python3 not on PATH")
	}
	for _, grade := range []string{"final", "transcript"} {
		t.Run(grade, func(t *testing.T) {
			agent := &chatAgent{}
			srv := httptest.NewServer(agent)
			defer srv.Close()
			port := portOf(t, srv.URL)
			dir, chat, ws := chatWorkspace(t, grade)
			script := filepath.Join(dir, "remotehttp.py")
			inst := filepath.Join(dir, "instruction.md")
			for p, c := range map[string]string{script: remoteLauncherSource, inst: "system: You are Acme support.\n\nuser: My grinder arrived cracked.\n"} {
				if err := os.WriteFile(p, []byte(c), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			answer := filepath.Join(ws, "answer.txt")
			cmd := exec.Command(py, "-I", script)
			cmd.Env = []string{"BENCH_REMOTE_INSTRUCTION=" + inst, "BENCH_ANSWER_PATH=" + answer, "BENCH_REMOTE_FILES_DIR=" + ws,
				"BENCH_REMOTE_CHAT=" + chat, "BENCH_REMOTE_URL=http://agent.invalid:" + port + "/run", utils.EnvRemoteConnect + "=127.0.0.1:" + port,
				"BENCH_REMOTE_SESSION_ID=r1", "BENCH_REMOTE_TIMEOUT_S=10"}
			out, err := cmd.CombinedOutput()
			if err != nil {
				t.Fatalf("%v\n%s", err, out)
			}
			if len(agent.bodies) != 2 {
				t.Fatalf("%d requests\n%s", len(agent.bodies), out)
			}
			first, second := agent.bodies[0], agent.bodies[1]
			if first["instruction"] != "My grinder arrived cracked." || len(first["messages"].([]any)) != 2 || first["session_id"] != "r1" {
				t.Fatalf("first request %v", first)
			}
			atts := first["attachments"].([]any)
			photo := atts[0].(map[string]any)
			if len(atts) != 2 || photo["content_base64"] != base64.StdEncoding.EncodeToString([]byte("\x89PNG")) || atts[1].(map[string]any)["url"] != "https://example.com/manual.pdf" {
				t.Fatalf("attachments %v", atts)
			}
			msgs := second["messages"].([]any)
			if second["instruction"] != "How long will the replacement take?" || len(msgs) != 4 || msgs[2].(map[string]any)["role"] != "assistant" || second["turn"] != float64(2) {
				t.Fatalf("second request %v", second)
			}
			got, _ := os.ReadFile(answer)
			switch grade {
			case "final":
				if string(got) != "About 3 days." {
					t.Fatalf("answer %q", got)
				}
			default:
				if !strings.HasPrefix(string(got), "system: You are Acme support.") || !strings.Contains(string(got), "assistant: About 3 days.") {
					t.Fatalf("transcript %q", got)
				}
			}
		})
	}
}

func TestOpenAIAgentLauncherConversation(t *testing.T) {
	ep := &fakeOpenAI{answerOf: func(body map[string]any) string {
		msgs := body["messages"].([]any)
		if len(msgs) > 3 {
			return "About 3 days."
		}
		return "A replacement is on its way."
	}}
	srv := httptest.NewServer(ep)
	defer srv.Close()
	port := portOf(t, srv.URL)
	dir, chat, ws := chatWorkspace(t, "final")
	env := map[string]string{
		"BENCH_OPENAI_URL": "http://agent.invalid:" + port + "/v1/chat/completions", utils.EnvRemoteConnect: "127.0.0.1:" + port,
		"BENCH_OPENAI_API": "chat", "BENCH_OPENAI_MODEL": "m", "BENCH_OPENAI_TIMEOUT_S": "20",
		"BENCH_OPENAI_CHAT": chat, "BENCH_OPENAI_FILES_DIR": ws,
	}
	answer, out, err := runOpenAILauncher(t, dir, "ignored", env)
	if err != nil || answer != "About 3 days." {
		t.Fatalf("answer %q err %v\n%s", answer, err, out)
	}
	if len(ep.bodies) != 2 {
		t.Fatalf("%d requests", len(ep.bodies))
	}
	first := ep.bodies[0]["messages"].([]any)
	if len(first) != 2 || first[0].(map[string]any)["role"] != "system" {
		t.Fatalf("first messages %v", first)
	}
	parts, ok := first[1].(map[string]any)["content"].([]any)
	if !ok || len(parts) != 3 || parts[0].(map[string]any)["text"] != "My grinder arrived cracked." {
		t.Fatalf("first user content %v", first[1])
	}
	img := parts[1].(map[string]any)["image_url"].(map[string]any)["url"].(string)
	if !strings.HasPrefix(img, "data:image/png;base64,") {
		t.Fatalf("image part %v", parts[1])
	}
	second := ep.bodies[1]["messages"].([]any)
	if len(second) != 4 || second[3].(map[string]any)["content"] != "How long will the replacement take?" || second[2].(map[string]any)["content"] != "A replacement is on its way." {
		t.Fatalf("second messages %v", second)
	}
}
