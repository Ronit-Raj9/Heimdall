package harbor

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPlanTestScriptTemplate(t *testing.T) {
	src, err := os.ReadFile(filepath.Join("testdata", "tb", "hello-file", "tests", "test.sh"))
	if err != nil {
		t.Fatal(err)
	}
	p, err := planTestScript(string(src))
	if err != nil {
		t.Fatalf("planTestScript: %v", err)
	}
	wantBake := []string{
		"apt-get update",
		"apt-get install -y curl",
		"curl -LsSf https://astral.sh/uv/0.9.5/install.sh | env UV_UNMANAGED_INSTALL=/opt/harbor-tests/uv sh",
		"export UV_PYTHON_INSTALL_DIR=/opt/harbor-tests/python UV_CACHE_DIR=/tmp/harbor-uv-cache && /opt/harbor-tests/uv/uv venv --python 3.13 /opt/harbor-tests/venv1 && " +
			"/opt/harbor-tests/uv/uv pip install --python /opt/harbor-tests/venv1/bin/python pytest==8.4.1 pytest-json-ctrf==0.3.5 && rm -rf /tmp/harbor-uv-cache && chmod -R a+rX /opt/harbor-tests",
	}
	if strings.Join(p.Bake, "\n") != strings.Join(wantBake, "\n") {
		t.Errorf("bake:\n%s\nwant:\n%s", strings.Join(p.Bake, "\n"), strings.Join(wantBake, "\n"))
	}
	for _, want := range []string{
		"#!/bin/bash\n# Changed by bifrost-bench import harbor",
		": # baked into the task image at build by bifrost-bench import harbor: apt-get update",
		": # baked into the task image at build by bifrost-bench import harbor: source $HOME/.local/bin/env",
		"\n/opt/harbor-tests/venv1/bin/pytest --ctrf /logs/verifier/ctrf.json /tests/test_outputs.py -rA\n",
		"echo 1 > /logs/verifier/reward.txt",
		`if [ "$PWD" = "/" ]; then`,
	} {
		if !strings.Contains(p.Script, want) {
			t.Errorf("script lacks %q:\n%s", want, p.Script)
		}
	}
	if strings.Contains(p.Script, "\nuvx") || strings.Contains(p.Script, "\ncurl ") {
		t.Errorf("script still downloads:\n%s", p.Script)
	}
	if len(p.Changes) != 4 {
		t.Errorf("changes = %q, want 4", p.Changes)
	}
}

func TestPlanTestScriptLines(t *testing.T) {
	cases := []struct {
		name     string
		src      string
		wantBake []string
		wantLine string // a line the rewritten script must hold
		wantErr  string
	}{
		{name: "pip with continuation and fallback", src: "pip3 install --break-system-packages \\\n  pytest==8.4.1 2>/dev/null || \\\n  pip install pytest==8.4.1\n",
			wantBake: []string{"pip3 install --break-system-packages pytest==8.4.1 2>/dev/null || pip install pytest==8.4.1"}},
		{name: "apt chain with quiet output", src: "apt-get update -qq && DEBIAN_FRONTEND=noninteractive apt-get install -y expect curl > /dev/null 2>&1\n",
			wantBake: []string{"apt-get update -qq && DEBIAN_FRONTEND=noninteractive apt-get install -y expect curl > /dev/null 2>&1"}},
		{name: "uvx inside if", src: "curl -LsSf https://astral.sh/uv/0.9.7/install.sh | sh\nif uvx --with pytest==8.4.1 pytest /verifier/t.py; then echo 1; fi\n",
			wantLine: "if /opt/harbor-tests/venv1/bin/pytest /verifier/t.py; then echo 1; fi"},
		{name: "uvx from", src: "curl -LsSf https://astral.sh/uv/install.sh | sh\nuvx --from ruff==0.6.0 ruff check .\n",
			wantLine: "/opt/harbor-tests/venv1/bin/ruff check ."},
		{name: "local service check is kept", src: "until curl -s http://localhost:3000 | grep -q 200; do sleep 1; done\n",
			wantLine: "until curl -s http://localhost:3000 | grep -q 200; do sleep 1; done"},
		{name: "plain commands are kept", src: "mkdir -p /logs/verifier\npytest /tests -rA\n", wantLine: "pytest /tests -rA"},
		{name: "uvx with the image's uv", src: "uvx -w pytest==8.4.1 pytest -rA\n", wantBake: []string{"export UV_PYTHON_INSTALL_DIR=/opt/harbor-tests/python UV_CACHE_DIR=/tmp/harbor-uv-cache && uv venv /opt/harbor-tests/venv1 && " +
			"uv pip install --python /opt/harbor-tests/venv1/bin/python pytest==8.4.1 && rm -rf /tmp/harbor-uv-cache && chmod -R a+rX /opt/harbor-tests"}, wantLine: "/opt/harbor-tests/venv1/bin/pytest -rA"},
		{name: "uvx with a package index", src: "uvx \\\n  --index https://download.pytorch.org/whl/cpu \\\n  --index-strategy unsafe-best-match \\\n  -p 3.13 -w torch==2.7.1 -w pytest==8.4.1 pytest /tests/t.py -rA\n",
			wantBake: []string{"export UV_PYTHON_INSTALL_DIR=/opt/harbor-tests/python UV_CACHE_DIR=/tmp/harbor-uv-cache && uv venv --python 3.13 /opt/harbor-tests/venv1 && " +
				"uv pip install --python /opt/harbor-tests/venv1/bin/python --index https://download.pytorch.org/whl/cpu --index-strategy unsafe-best-match torch==2.7.1 pytest==8.4.1 && rm -rf /tmp/harbor-uv-cache && chmod -R a+rX /opt/harbor-tests"},
			wantLine: "/opt/harbor-tests/venv1/bin/pytest /tests/t.py -rA"},
		{name: "uvx with a variable", src: "curl -LsSf https://astral.sh/uv/0.9.5/install.sh | sh\nuvx -w git+https://x/y@${COMMIT} pytest\n", wantErr: "shell variables"},
		{name: "uvx unknown option", src: "curl -LsSf https://astral.sh/uv/0.9.5/install.sh | sh\nuvx --with-requirements r.txt pytest\n", wantErr: "uvx option --with-requirements"},
		{name: "git clone", src: "git clone https://github.com/x/y src\n", wantErr: "package or source download"},
		{name: "data download", src: "curl -L -o weights.pt https://github.com/x/y/raw/w.pt\n", wantErr: "download from github.com"},
		{name: "uv project", src: "uv init --python 3.12\nuv add pytest\n", wantErr: "uv project commands"},
		{name: "pip reading test files", src: "pip install -r /tests/requirements.txt\n", wantErr: "package or source download"},
		{name: "npm install", src: "cd /app && npm install\n", wantErr: "package or source download"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			p, err := planTestScript(c.src)
			if c.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), c.wantErr) {
					t.Fatalf("got %v, want error containing %q", err, c.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("planTestScript: %v", err)
			}
			if c.wantBake != nil && strings.Join(p.Bake, "\n") != strings.Join(c.wantBake, "\n") {
				t.Errorf("bake = %q, want %q", p.Bake, c.wantBake)
			}
			if c.wantLine != "" && !strings.Contains(p.Script, c.wantLine+"\n") {
				t.Errorf("script lacks %q:\n%s", c.wantLine, p.Script)
			}
		})
	}
}

func TestSolutionFetch(t *testing.T) {
	cases := []struct {
		name, src, want string
	}{
		{"offline", "#!/bin/bash\n# apt-get install in a comment\ncat x > /app/y\n", ""},
		{"apt", "apt update\napt install -y stockfish\n", "apt install -y stockfish"},
		{"pip", "pip3 install numpy==2.3.2 --break-system-packages\n", "pip3 install numpy==2.3.2 --break-system-packages"},
		{"curl", "curl -fsSL https://example.com/x.tar.gz | tar xz\n", "curl -fsSL https://example.com/x.tar.gz | tar xz"},
		{"local curl", "curl -s http://localhost:8000/health\n", ""},
		{"git clone continued", "git \\\n  clone https://github.com/x/y\n", "git clone https://github.com/x/y"},
		{"uv run", "uv run python solve.py\n", "uv run python solve.py"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := solutionFetch(c.src); got != c.want {
				t.Errorf("got %q, want %q", got, c.want)
			}
		})
	}
}

func TestLogicalLinesSkipsHeredocBodies(t *testing.T) {
	src := "cat > f.py <<'EOF'\nimport os\napt-get install -y x\nEOF\necho done\n"
	lines := logicalLines(strings.Split(src, "\n"))
	if len(lines) != 3 || lines[0].last != 3 || lines[1].text != "echo done" {
		t.Fatalf("lines = %+v", lines)
	}
	if got := solutionFetch(src); got != "" {
		t.Errorf("solutionFetch read a heredoc body: %q", got)
	}
}
