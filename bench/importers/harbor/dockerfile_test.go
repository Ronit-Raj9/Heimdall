package harbor

import (
	"strings"
	"testing"
)

func TestParseDockerfile(t *testing.T) {
	cases := []struct {
		name, src string
		want      dockerfileInfo
	}{
		{"simple", "FROM ubuntu:24.04\nWORKDIR /app\n", dockerfileInfo{Workdir: "/app", Stages: 1}},
		{"relative workdir and user", "FROM python:3.12\nWORKDIR /srv/\nworkdir project\nUSER worker\n", dockerfileInfo{Workdir: "/srv/project", User: "worker", Stages: 1}},
		{"final stage only", "FROM golang AS build\nWORKDIR /src\nUSER builder\nFROM debian\nWORKDIR /app\n", dockerfileInfo{Workdir: "/app", Stages: 2}},
		{"variable workdir", "FROM ubuntu\nARG D=/x\nWORKDIR $D\n", dockerfileInfo{Stages: 1}},
		{"none", "FROM ubuntu\nRUN true\n", dockerfileInfo{Stages: 1}},
		{"continuation and comment", "FROM ubuntu\nRUN apt-get update && \\\n# a comment\n    apt-get install -y x\nWORKDIR \"/root\"\n", dockerfileInfo{Workdir: "/root", Stages: 1}},
		{"heredoc body ignored", "FROM ubuntu\nWORKDIR /app\nRUN <<EOF\nWORKDIR /nope\nEOF\n", dockerfileInfo{Workdir: "/app", Stages: 1}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := parseDockerfile(c.src); got != c.want {
				t.Errorf("got %+v, want %+v", got, c.want)
			}
		})
	}
}

func TestStripSkillCopies(t *testing.T) {
	src := "FROM ubuntu\nCOPY skills /root/.claude/skills\nCOPY --chown=1000 ./skills/ /home/a/.codex/skills\nCOPY skillset /x\n# COPY skills /root/.gemini/skills\nCOPY data /data\n"
	got, n := stripSkillCopies(src)
	if n != 2 {
		t.Fatalf("disabled %d lines, want 2:\n%s", n, got)
	}
	for _, l := range strings.Split(got, "\n") {
		if skillCopyRE.MatchString(l) {
			t.Errorf("skills copy still active: %q", l)
		}
	}
	if !strings.Contains(got, "\nCOPY skillset /x\n") || !strings.Contains(got, "\nCOPY data /data\n") {
		t.Errorf("other lines changed:\n%s", got)
	}
}

func TestBakeSection(t *testing.T) {
	got := bakeSection([]string{"apt-get update", "pip install x"}, []string{"/logs/verifier", "/tests"}, map[string]string{"B": `say "hi" $HOME`, "A": "1"}, "worker")
	want := []string{
		"USER root\n",
		"RUN apt-get update\nRUN pip install x\n",
		"git config --system --add safe.directory '*'",
		"RUN mkdir -p /logs/verifier /tests && chmod 0777 /logs/verifier /tests\n",
		"ENV A=\"1\"\nENV B=\"say \\\"hi\\\" \\$HOME\"\n",
		"USER worker\n",
	}
	for _, w := range want {
		if !strings.Contains(got, w) {
			t.Errorf("section lacks %q:\n%s", w, got)
		}
	}
	if strings.Contains(bakeSection(nil, nil, nil, ""), "USER root\nUSER") {
		t.Error("no final USER expected without one upstream")
	}
}
