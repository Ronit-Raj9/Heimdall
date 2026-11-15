package mcpmark

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestUpstreamTasks(t *testing.T) {
	up, err := openUpstream(filepath.Join("testdata", "upstream"), fixtureRef)
	if err != nil {
		t.Fatal(err)
	}
	tasks, err := up.tasks([]string{"filesystem", "postgres", "missing_family"})
	if err != nil {
		t.Fatal(err)
	}
	var ids []string
	for _, tk := range tasks {
		ids = append(ids, tk.upstreamID())
	}
	want := "filesystem/easy/file_context/count_demo,filesystem/standard/file_context/uppercase_demo,filesystem/standard/papers/missing_state,postgres/standard/chinook/rename_table"
	if strings.Join(ids, ",") != want {
		t.Fatalf("tasks = %v", ids)
	}
	if tasks[0].Meta.TaskName != "Count Demo" || tasks[0].category() != "file_context" {
		t.Fatalf("meta = %+v", tasks[0].Meta)
	}
}

func TestGitHead(t *testing.T) {
	const sha = "0123456789abcdef0123456789abcdef01234567"
	tests := []struct {
		name    string
		files   map[string]string
		want    string
		wantErr bool
	}{
		{name: "detached", files: map[string]string{"HEAD": sha + "\n"}, want: sha},
		{name: "loose ref", files: map[string]string{"HEAD": "ref: refs/heads/main\n", "refs/heads/main": sha + "\n"}, want: sha},
		{name: "packed ref", files: map[string]string{"HEAD": "ref: refs/heads/main\n", "packed-refs": "# pack-refs\n" + sha + " refs/heads/main\n"}, want: sha},
		{name: "ref escapes", files: map[string]string{"HEAD": "ref: ../../etc/passwd\n"}, wantErr: true},
		{name: "ref missing", files: map[string]string{"HEAD": "ref: refs/heads/gone\n"}, wantErr: true},
		{name: "no git", files: map[string]string{}, wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			root := t.TempDir()
			for name, body := range tt.files {
				p := filepath.Join(root, ".git", filepath.FromSlash(name))
				if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			got, err := gitHead(root)
			if (err != nil) != tt.wantErr || got != tt.want {
				t.Fatalf("got %q, %v", got, err)
			}
		})
	}
}
