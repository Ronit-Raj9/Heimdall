package main

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/maximhq/bifrost/bench/agentinfer"
	"github.com/maximhq/bifrost/bench/handlers"
)

const cliTraceExport = `{"resourceSpans":[{"resource":{"attributes":[{"key":"service.name","value":{"stringValue":"support-svc"}}]},
"scopeSpans":[{"scope":{"name":"openinference.instrumentation.langchain"},"spans":[
{"traceId":"0af7651916cd43dd8448eb211c80319c","spanId":"a000000000000001","name":"POST /chat","kind":2,"attributes":[
 {"key":"http.request.method","value":{"stringValue":"POST"}},{"key":"http.route","value":{"stringValue":"/chat"}},
 {"key":"server.address","value":{"stringValue":"support.example.com"}}]},
{"traceId":"0af7651916cd43dd8448eb211c80319c","spanId":"a000000000000002","parentSpanId":"a000000000000001","name":"invoke_agent","attributes":[
 {"key":"gen_ai.operation.name","value":{"stringValue":"invoke_agent"}},{"key":"gen_ai.agent.name","value":{"stringValue":"support-bot"}}]},
{"traceId":"0af7651916cd43dd8448eb211c80319c","spanId":"a000000000000003","parentSpanId":"a000000000000002","name":"chat","attributes":[
 {"key":"gen_ai.operation.name","value":{"stringValue":"chat"}},{"key":"gen_ai.request.model","value":{"stringValue":"gpt-5.4-mini"}}]}
]}]}]}`

func TestAgentFileFromTracesCheckAndLocalPush(t *testing.T) {
	dir := t.TempDir()
	export := filepath.Join(dir, "export.json")
	os.WriteFile(export, []byte(cliTraceExport), 0o644)
	af := filepath.Join(dir, "repo", agentinfer.AgentFilePath)
	var out bytes.Buffer
	if code := runAgentFile([]string{"from-traces", export, "-o", af}, &out); code != exitOK {
		t.Fatalf("from-traces %d: %s", code, out.String())
	}
	if !strings.Contains(out.String(), "support-bot, langchain, gpt-5.4-mini") || !strings.Contains(out.String(), "seen in your traces: model = gpt-5.4-mini") {
		t.Fatalf("%s", out.String())
	}
	if code := runAgentFile([]string{"from-traces", export, "-o", af}, &out); code == exitOK {
		t.Fatal("overwrote without -force")
	}
	out.Reset()
	if code := runAgentFile([]string{"check", "-f", af}, &out); code != exitOK || !strings.Contains(out.String(), "template http-url, model openai gpt-5.4-mini") {
		t.Fatalf("check %d: %s", code, out.String())
	}
	project := filepath.Join(dir, "project")
	cfg := filepath.Join(project, "deploy", "bench.yaml")
	out.Reset()
	if code := runAgentFile([]string{"push", "-f", af, "-config", cfg}, &out); code != exitOK {
		t.Fatalf("push %d: %s", code, out.String())
	}
	b, err := os.ReadFile(filepath.Join(project, "agents", "support-bot.yaml"))
	if err != nil || !strings.Contains(string(b), "https://support.example.com/chat") || !strings.Contains(string(b), "remote_http") {
		t.Fatalf("%v %s", err, b)
	}
	if code := runAgentFile([]string{"push", "-f", af, "-config", cfg}, &out); code == exitOK {
		t.Fatal("local push overwrote without -force")
	}
	bad := filepath.Join(dir, "bad.yaml")
	os.WriteFile(bad, []byte("id: x\nconnect: {template: http-url, token: abc}\n"), 0o644)
	out.Reset()
	if code := runAgentFile([]string{"check", "-f", bad}, &out); code == exitOK || !strings.Contains(out.String(), "never holds them") {
		t.Fatalf("bad check %d: %s", code, out.String())
	}
}

func TestAgentFilePushServerUpdatesExisting(t *testing.T) {
	var calls []string
	var put handlers.AgentConfigPut
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		calls = append(calls, r.Method+" "+r.URL.Path)
		switch {
		case r.Method == "POST" && r.URL.Path == "/api/bench/agents/from-template":
			var req agentinfer.FromTemplate
			json.Unmarshal(body, &req)
			if !req.DryRun {
				w.WriteHeader(http.StatusConflict)
				io.WriteString(w, `{"error":{"message":"an agent named bot already exists"}}`)
				return
			}
			json.NewEncoder(w).Encode(handlers.AgentFromTemplateResponse{ID: "bot", YAML: "id: bot\nadapter: remote_http\n", DryRun: true})
		case r.Method == "PUT" && r.URL.Path == "/api/bench/agents/bot/config":
			json.Unmarshal(body, &put)
			io.WriteString(w, `{}`)
		default:
			w.WriteHeader(404)
		}
	}))
	defer srv.Close()
	af := filepath.Join(t.TempDir(), "agent.yaml")
	os.WriteFile(af, []byte("id: bot\nconnect: {template: http-url, url: 'https://bot.example.com/run'}\n"), 0o644)
	var out bytes.Buffer
	if code := runAgentFile([]string{"push", "-f", af, "-server", srv.URL, "-token", "hma_test"}, &out); code != exitOK {
		t.Fatalf("%d %s %v", code, out.String(), calls)
	}
	if !strings.Contains(out.String(), "updated agent bot") || put.YAML != "id: bot\nadapter: remote_http\n" || len(calls) != 3 {
		t.Fatalf("%s %v %+v", out.String(), calls, put)
	}
}
