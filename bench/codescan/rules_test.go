package codescan

import (
	"encoding/json"
	"fmt"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/maximhq/bifrost/bench/utils"
)

// addedFileDiff is the git diff that adds a new file with content.
func addedFileDiff(p, content string) string {
	lines := strings.Split(strings.TrimSuffix(content, "\n"), "\n")
	var b strings.Builder
	fmt.Fprintf(&b, "diff --git a/%s b/%s\nnew file mode 100644\nindex 0000000..1111111\n--- /dev/null\n+++ b/%s\n@@ -0,0 +1,%d @@\n", p, p, p, len(lines))
	for _, l := range lines {
		b.WriteString("+" + l + "\n")
	}
	return b.String()
}

// replaceDiff is the git diff of a file whose first lines old became new,
// followed by one unchanged line.
func replaceDiff(p, old, new string) string {
	ol := strings.Split(strings.TrimSuffix(old, "\n"), "\n")
	nl := strings.Split(strings.TrimSuffix(new, "\n"), "\n")
	var b strings.Builder
	fmt.Fprintf(&b, "diff --git a/%s b/%s\nindex 1111111..2222222 100644\n--- a/%s\n+++ b/%s\n@@ -1,%d +1,%d @@\n", p, p, p, p, len(ol)+1, len(nl)+1)
	for _, l := range ol {
		b.WriteString("-" + l + "\n")
	}
	for _, l := range nl {
		b.WriteString("+" + l + "\n")
	}
	b.WriteString(" keep = 1\n")
	return b.String()
}

func ruleIDs(ev *schemas.CodeScanEvidence) []string {
	var out []string
	for _, f := range ev.Findings {
		out = append(out, f.Rule)
	}
	return out
}

func TestRuleTable(t *testing.T) {
	cwe := regexp.MustCompile(`^CWE-[0-9]+$`)
	id := regexp.MustCompile(`^[a-z][a-z0-9-]+$`)
	known := []string{langAny, LangPython, LangJavaScript, LangTypeScript, LangGo, LangShell, LangDockerfile, LangYAML, LangMake, LangConfig}
	seen := map[string]bool{}
	for _, r := range Rules() {
		if !id.MatchString(r.ID) || seen[r.ID] {
			t.Errorf("rule id %q malformed or duplicate", r.ID)
		}
		seen[r.ID] = true
		if r.Title == "" || !cwe.MatchString(r.CWE) || schemas.CodeScanSeverityRank(r.Severity) == 0 {
			t.Errorf("%s: title, CWE or severity missing: %+v", r.ID, r)
		}
		if !strings.HasSuffix(r.Summary, ".") || len(r.Languages) == 0 || r.match == nil {
			t.Errorf("%s: summary, languages or match missing", r.ID)
		}
		for _, l := range r.Languages {
			if !slices.Contains(known, l) {
				t.Errorf("%s: unknown language %q", r.ID, l)
			}
		}
		if r.Fixture.Path == "" || r.Fixture.Bad == "" || r.Fixture.Good == "" {
			t.Errorf("%s: fixture incomplete", r.ID)
		}
		if strings.ContainsAny(r.Title+r.Summary, "\u2013\u2014") {
			t.Errorf("%s: dash character in text", r.ID)
		}
	}
	if len(Describe()) != len(rules) {
		t.Fatal("Describe lost rules")
	}
}

// TestRuleFixtures checks every rule fires on its bad fixture and that its
// good fixture raises no finding of any rule.
func TestRuleFixtures(t *testing.T) {
	for _, r := range Rules() {
		t.Run(r.ID, func(t *testing.T) {
			var bad, good string
			if r.Removed {
				bad = replaceDiff(r.Fixture.Path, r.Fixture.Bad, r.Fixture.Good)
				good = replaceDiff(r.Fixture.Path, r.Fixture.Good, r.Fixture.Good+"\n# unchanged")
			} else {
				bad = addedFileDiff(r.Fixture.Path, r.Fixture.Bad)
				good = addedFileDiff(r.Fixture.Path, r.Fixture.Good)
			}
			ev := Scan([]byte(bad))
			if !ev.Scanned || !slices.Contains(ruleIDs(ev), r.ID) {
				t.Errorf("bad fixture: got %v (%s), want %s", ruleIDs(ev), ev.Reason, r.ID)
			}
			if ev := Scan([]byte(good)); len(ev.Findings) != 0 {
				t.Errorf("good fixture: got %v, want none", ruleIDs(ev))
			}
		})
	}
}

// TestRuleCases covers each rule in every language it reads, with the near
// misses that must stay quiet.
func TestRuleCases(t *testing.T) {
	tests := []struct {
		name, path, content, want string // want "" means no finding at all
	}{
		// Secrets.
		{"aws key in yaml", "deploy/app.yaml", "aws_access_key_id: AKIAIOSFODNN7ABCDEFG\n", "secret-token"},
		{"github token in js", "src/gh.js", "const gh = 'ghp_" + "abcdefghijklmnopqrstuvwxyz0123';\n", "secret-token"},
		{"slack token in go", "bot/main.go", "package main\n\nvar t = \"xoxb-" + "1234567890-abcdefghij\"\n", "secret-token"},
		{"high entropy key", ".env", "STRIPE_API_KEY=Zq8vN2pL5xR7tW1yK4mB9cF3\n", "secret-token"},
		{"high entropy key in code", "app/k.py", "API_KEY = \"Zq8vN2pL5xR7tW1yK4mB9cF3\"\n", "secret-token"},
		{"key name assigned expression", "app/k.py", "target_key = setting_path[-1]\n", ""},
		// Misfires found on real code and fixed (docs/code-scan.md, precision).
		{"storage key name", "src/s.ts", "const COLLAPSE_KEY = \"heimdall.nav-collapsed\";\nexport const WRITE_TOKEN_KEY = \"bifrost-bench.write-token\";\n", ""},
		{"env reference in json", "deploy/config.json", "{\"setup_token\": \"env.BIFROST_SETUP_TOKEN\", \"apiKey\": \"{env:BIFROST_API_KEY}\"}\n", ""},
		{"printf format", "spike/run.sh", "printf 'BIFROST_SETUP_TOKEN=%s\\nX=%s\\n' a b\n", ""},
		{"fake key in a test", "pkg/x_test.go", "package x\n\nconst k = \"sk-" + "ant-api03-abcdefghijklmnop\"\n", ""},
		{"curl pipe in testdata", "importers/testdata/t/test.sh", "#!/bin/sh\ncurl -LsSf https://astral.sh/uv/install.sh | sh\n", ""},
		{"escaped backticks", "ci/check.sh", "#!/bin/bash\necho \"| Verdict | \\`$verdict\\` |\"\n", ""},
		{"compose password", "spike/compose.yaml", "services:\n  db:\n    environment:\n      POSTGRES_PASSWORD: bifrost-spike-pw\n", "secret-assignment"},
		{"helm secret name", "ci/validate.sh", "#!/bin/sh\nhelm template x --set bifrost.authConfig.existingSecret=admin-secret \\\n", ""},
		{"helm admin password", "ci/validate.sh", "#!/bin/sh\nhelm template x --set bifrost.authConfig.adminPassword=governance-pass \\\n", "secret-assignment"},
		{"not a real key", "examples/s.yaml", "caller_vk: sk-" + "bf-not-a-real-key-0123456789\n", ""},
		{"word key", "cli/scripts/configure.py", "KEY = \"sk-" + "bf-subprocess-integration\"\n", ""},
		{"sentinel token", "cli/scripts/configure.py", "env = dict(BIFROST_AGENT_TOKEN=\"sso-must-not-be-persisted\")\n", ""},
		{"locale label", "ui/locales/de/config.json", "{\"password\": \"Passwort\", \"apiKey\": \"API-Schl\u00fcssel\"}\n", ""},
		{"label value", "app/forms.py", "LABELS = {\"password\": \"Password\"}\n", ""},
		{"key placeholder", "ui/auth.tsx", "<input placeholder=\"-----BEGIN PRIVATE KEY----- or env.IDP_PRIVATE_KEY\" />\n", ""},
		{"inline key", "deploy/sa.json", "{\"private_key\": \"-----BEGIN PRIVATE KEY-----\\nMIIEvQIBADANBgkqhkiG9w0BAQEFAASCBKcwggSjAgEAAoIBAQC7\\n-----END PRIVATE KEY-----\\n\"}\n", "secret-private-key"},
		{"make parameter expansion", "Makefile", "run:\n\t$${CHATGPT_ACCESS_TOKEN:+--env-var \"tok=$$CHATGPT_ACCESS_TOKEN\"} \\\n", ""},
		{"oauth urn", "core/oauth.go", "package core\n\nconst TokenTypeIDToken = \"urn:ietf:params:oauth:token-type:id_token\"\n", ""},
		{"docker socket", "scripts/deps.sh", "#!/bin/sh\nsudo chmod 666 /var/run/docker.sock\n", "world-writable-permissions"},
		{"heredoc python", "ci/cost.sh", "#!/bin/sh\npython3 - <<EOF\nsetup_token = sys.argv[9]\nEOF\n", ""},
		{"upper-case marker", "core/fixture.go", "package core\n\nconst ExportFixtureSecret = \"BIFROST-EXPORT-SECRET-CANARY\"\n", ""},
		{"env lookup", "src/cfg.ts", "const apiKey = process.env.API_KEY;\n", ""},
		{"password literal in go", "db/db.go", "package db\n\nvar cfg = Config{Password: \"s3cr3t-Pa55word\"}\n", "secret-assignment"},
		{"password in env file", ".env", "DB_PASSWORD=hunter2hunter2\n", "secret-assignment"},
		{"password placeholder", ".env.example", "DB_PASSWORD=changeme\n", ""},
		{"password from form", "app/login.py", "password = request.form['password']\n", ""},
		{"token type annotation", "app/t.py", "def f(token: str = None):\n    return token\n", ""},
		{"token env name", "app/t.py", "TOKEN_VAR = 'GITHUB_TOKEN'\ntoken = 'GITHUB_TOKEN'\n", ""},
		{"password in test", "tests/test_login.py", "PASSWORD = \"correct-horse-battery\"\n", ""},
		{"token in comment", "app/x.py", "# api_key = \"9f8e7d6c5b4a39281706\"\nx = 1\n", "secret-token"},
		{"password in comment", "app/x.py", "# password = \"Tr0ub4dor&3x\"\nx = 1\n", ""},
		// eval.
		{"py exec of file", "tools/x.py", "exec(open(path).read())\n", "code-eval"},
		{"py eval const", "tools/x.py", "x = eval(\"1 + 2\")\n", ""},
		{"py def eval", "tools/x.py", "class M:\n    def eval(self, x):\n        return x\n", ""},
		{"js eval", "src/x.js", "const v = eval(userInput);\n", "code-eval"},
		{"js new Function", "src/x.ts", "const fn = new Function('a', body);\n", "code-eval"},
		{"js new Function const", "src/x.ts", "const g = new Function('return this')();\n", ""},
		{"js vm", "src/x.js", "vm.runInNewContext(code, sandbox);\n", "code-eval"},
		{"js regex exec", "src/x.js", "const m = re.exec(s);\n", ""},
		{"eval in string", "src/x.js", "log('do not call eval(x) here');\n", ""},
		// Shell.
		{"py os.system", "app/x.py", "os.system(f\"rm -rf {path}\")\n", "shell-injection-python"},
		{"py os.system const", "app/x.py", "os.system(\"clear\")\n", ""},
		{"py shell true multiline", "app/x.py", "subprocess.check_output(\n    cmd,\n    shell=True,\n)\n", "shell-injection-python"},
		{"py shell true in test", "tests/test_x.py", "subprocess.run(cmd, shell=True)\n", ""},
		{"py shell true commented", "app/x.py", "# subprocess.run(cmd, shell=True)\nx = 1\n", ""},
		{"js exec concat", "src/x.ts", "import { exec } from 'node:child_process';\nexec('git log ' + branch);\n", "shell-injection-js"},
		{"js exec tainted var", "src/x.js", "const cp = require('child_process');\nconst cmd = `tar -xf ${file}`;\ncp.exec(cmd);\n", "shell-injection-js"},
		{"js exec const", "src/x.js", "const cp = require('child_process');\ncp.exec('ls -la', cb);\n", ""},
		{"js spawn shell", "src/x.js", "const cp = require('child_process');\ncp.spawn(`convert ${f}`, { shell: true });\n", "shell-injection-js"},
		{"go sh -c sprintf", "x/x.go", "package x\n\nfunc f(n string) {\n\texec.Command(\"bash\", \"-c\", fmt.Sprintf(\"ls %s\", n))\n}\n", "shell-injection-go"},
		{"go ctx sh -c", "x/x.go", "package x\n\nfunc f(ctx context.Context, n string) {\n\texec.CommandContext(ctx, \"sh\", \"-c\", n)\n}\n", "shell-injection-go"},
		{"sh -c var", "scripts/x.sh", "#!/bin/bash\nbash -c \"$USER_CMD\"\n", "shell-eval-sh"},
		{"backtick var command", "scripts/x.sh", "#!/bin/bash\nout=`$cmd --flag`\n", "shell-eval-sh"},
		{"dollar paren var", "Makefile", "run:\n\tout=$( $(CMD) )\n", ""},
		{"eval const", "scripts/x.sh", "#!/bin/sh\neval \"export A=1\"\n", ""},
		// SQL.
		{"py format sql", "app/x.py", "cur.execute(\"SELECT * FROM t WHERE id = '%s'\" % uid)\n", "sql-string-build"},
		{"py dot format", "app/x.py", "db.execute(\"DELETE FROM t WHERE id = {}\".format(uid))\n", "sql-string-build"},
		{"py concat", "app/x.py", "cursor.execute(\"SELECT name FROM users WHERE id=\" + uid)\n", "sql-string-build"},
		{"py param", "app/x.py", "cur.execute(\"SELECT * FROM t WHERE id = ?\", (uid,))\n", ""},
		{"py f-string table only", "app/x.py", "cur.execute(f\"SELECT * FROM {table} WHERE id = ?\", (uid,))\n", "sql-string-build"},
		{"js template sql", "src/db.ts", "await pool.query(`SELECT * FROM users WHERE email = '${email}'`);\n", "sql-string-build"},
		{"js param sql", "src/db.ts", "await pool.query('SELECT * FROM users WHERE email = $1', [email]);\n", ""},
		{"js tagged template", "src/db.ts", "await prisma.$queryRaw`SELECT * FROM users WHERE id = ${id}`;\n", ""},
		{"go sprintf sql", "db/q.go", "package db\n\nfunc f(n string) {\n\tq := fmt.Sprintf(\"SELECT * FROM users WHERE name = '%s'\", n)\n\trows, _ := db.Query(q)\n\t_ = rows\n}\n", "sql-string-build"},
		{"go param sql", "db/q.go", "package db\n\nfunc f(n string) {\n\trows, _ := db.QueryContext(ctx, \"SELECT * FROM users WHERE name = $1\", n)\n\t_ = rows\n}\n", ""},
		{"py non-sql execute", "app/x.py", "executor.execute(task + suffix)\n", ""},
		// TLS.
		{"go insecure skip", "net/c.go", "package net\n\nvar c = &tls.Config{InsecureSkipVerify: true}\n", "tls-verify-disabled"},
		{"js reject unauthorized", "src/c.ts", "const agent = new https.Agent({ rejectUnauthorized: false });\n", "tls-verify-disabled"},
		{"node tls env in dockerfile", "Dockerfile", "FROM node:22\nENV NODE_TLS_REJECT_UNAUTHORIZED=0\n", "tls-verify-disabled"},
		{"node tls env in js", "src/x.js", "process.env.NODE_TLS_REJECT_UNAUTHORIZED = '0';\n", "tls-verify-disabled"},
		{"curl -k", "scripts/x.sh", "#!/bin/sh\ncurl -sSk https://internal/health\n", "tls-verify-disabled"},
		{"curl -o", "scripts/x.sh", "#!/bin/sh\ncurl -sS -o out.json https://internal/health\n", ""},
		{"yaml skip verify", "deploy/prom.yml", "tls_config:\n  insecure_skip_verify: true\n", "tls-verify-disabled"},
		{"py verify false not http", "app/x.py", "def check(data, verify=False):\n    return data\n", ""},
		{"py unverified ctx", "app/x.py", "ctx = ssl._create_unverified_context()\n", "tls-verify-disabled"},
		{"tls in test file", "tests/test_c.py", "r = requests.get(url, verify=False)\n", ""},
		// Weak hash.
		{"js md5 password", "src/auth.js", "const h = crypto.createHash('md5').update(password).digest('hex');\n", "weak-password-hash"},
		{"go sha1 password", "auth/a.go", "package auth\n\nfunc hash(password string) []byte {\n\ts := sha1.Sum([]byte(password))\n\treturn s[:]\n}\n", "weak-password-hash"},
		{"js md5 etag", "src/etag.js", "const etag = crypto.createHash('md5').update(body).digest('hex');\n", ""},
		{"py sha256 password", "app/a.py", "h = hashlib.sha256(password.encode()).hexdigest()\n", ""},
		// Deserialization.
		{"pickle loads", "app/x.py", "obj = pickle.loads(blob)\n", "insecure-deserialization"},
		{"yaml unsafe loader", "app/x.py", "cfg = yaml.load(f, Loader=yaml.Loader)\n", "insecure-deserialization"},
		{"yaml safe_load", "app/x.py", "cfg = yaml.safe_load(f)\n", ""},
		{"yaml loader next line", "app/x.py", "cfg = yaml.load(\n    f, Loader=yaml.CSafeLoader\n)\n", ""},
		{"marshal", "app/x.py", "code = marshal.loads(data)\n", "insecure-deserialization"},
		{"json loads", "app/x.py", "obj = json.loads(blob)\n", ""},
		{"node-serialize", "src/x.js", "const serialize = require('node-serialize');\nconst o = serialize.unserialize(body);\n", "insecure-deserialization"},
		// CORS.
		{"express cors", "src/app.js", "app.use(cors({ origin: true, credentials: true }));\n", "cors-credentials-any-origin"},
		{"express cors list", "src/app.js", "app.use(cors({ origin: ['https://a.example'], credentials: true }));\n", ""},
		{"go cors", "srv/s.go", "package srv\n\nvar c = cors.Config{AllowOrigins: []string{\"*\"}, AllowCredentials: true}\n", "cors-credentials-any-origin"},
		{"flask cors no origins", "app/x.py", "CORS(app, supports_credentials=True)\n", "cors-credentials-any-origin"},
		{"flask cors origins", "app/x.py", "CORS(app, origins=[\"https://a.example\"], supports_credentials=True)\n", ""},
		{"header echo", "srv/s.go", "package srv\n\nfunc h(w http.ResponseWriter, r *http.Request) {\n\tw.Header().Set(\"Access-Control-Allow-Origin\", r.Header.Get(\"Origin\"))\n\tw.Header().Set(\"Access-Control-Allow-Credentials\", \"true\")\n}\n", "cors-credentials-any-origin"},
		{"any origin no credentials", "src/app.js", "app.use(cors({ origin: '*' }));\n", ""},
		// Permissions.
		{"chmod 777 sh", "scripts/x.sh", "#!/bin/sh\nchmod 777 /var/run/app.sock\n", "world-writable-permissions"},
		{"chmod a+rwx yaml", ".github/workflows/ci.yml", "steps:\n  - run: chmod a+rwx ./bin\n", "world-writable-permissions"},
		{"py chmod", "app/x.py", "os.chmod(path, 0o777)\n", "world-writable-permissions"},
		{"go chmod", "x/x.go", "package x\n\nfunc f() { os.Chmod(\"/tmp/x\", 0777) }\n", "world-writable-permissions"},
		{"go mkdirall 0777", "x/x.go", "package x\n\nfunc f() { os.MkdirAll(dir, 0777) }\n", ""},
		{"chmod +x", "scripts/x.sh", "#!/bin/sh\nchmod +x run.sh\n", ""},
		// Remote script.
		{"wget pipe bash dockerfile", "Dockerfile", "FROM ubuntu:24.04\nRUN wget -qO- https://x.example/setup | sudo -E bash -\n", "curl-pipe-shell"},
		{"bash process substitution", "scripts/x.sh", "#!/bin/bash\nbash <(curl -s https://x.example/i.sh)\n", "curl-pipe-shell"},
		{"curl pipe python yaml", ".github/workflows/ci.yml", "steps:\n  - run: curl -sSL https://install.python-poetry.org | python3 -\n", "curl-pipe-shell"},
		{"curl pipe jq", "scripts/x.sh", "#!/bin/sh\ncurl -s https://api.example/x | jq .name\n", ""},
		{"curl pipe in comment", "scripts/x.sh", "#!/bin/sh\n# curl https://x | sh\necho ok\n", ""},
		{"curl pipe in py string", "tools/x.py", "subprocess.run(\"curl -s https://x.example/i.sh | sh\", shell=True)\n", "curl-pipe-shell"},
		// Debug.
		{"fastapi debug", "app/main.py", "app = FastAPI(debug=True)\n", "debug-mode-enabled"},
		{"django debug", "proj/settings.py", "DEBUG = True\n", "debug-mode-enabled"},
		{"debug const elsewhere", "app/consts.py", "DEBUG = True\n", ""},
		{"flask debug env", "Dockerfile", "FROM python:3.12\nENV FLASK_DEBUG=1\n", "debug-mode-enabled"},
		{"gin debug", "srv/m.go", "package srv\n\nfunc init() { gin.SetMode(gin.DebugMode) }\n", "debug-mode-enabled"},
		{"pprof import", "srv/m.go", "package srv\n\nimport (\n\t_ \"net/http/pprof\"\n)\n", "debug-mode-enabled"},
		// CSRF.
		{"wtf csrf off", "app/config.py", "WTF_CSRF_ENABLED = False\n", "csrf-disabled"},
		{"flask config csrf off", "app/config.py", "app.config[\"WTF_CSRF_ENABLED\"] = False\n", "csrf-disabled"},
		{"js csrf false", "src/server.ts", "const app = createServer({ csrf: false });\n", "csrf-disabled"},
		{"csrf in test", "tests/conftest.py", "app.config[\"WTF_CSRF_ENABLED\"] = False\n", ""},
		// Path traversal.
		{"flask send_file", "app/x.py", "return send_file(request.args[\"path\"])\n", "path-traversal"},
		{"flask route param", "app/x.py", "@app.route(\"/f/<path:name>\")\ndef f(name):\n    return open(os.path.join(ROOT, name)).read()\n", "path-traversal"},
		{"express readFile", "src/x.js", "app.get('/f', (req, res) => {\n  fs.readFile(path.join(dir, req.query.name), cb);\n});\n", "path-traversal"},
		{"express destructure", "src/x.js", "const { name } = req.params;\nres.sendFile(path.join(dir, name));\n", "path-traversal"},
		{"express sendFile root", "src/x.js", "res.sendFile(req.params.name, { root: dir });\n", ""},
		{"go open form value", "srv/f.go", "package srv\n\nfunc h(w http.ResponseWriter, r *http.Request) {\n\tname := r.URL.Query().Get(\"f\")\n\tb, _ := os.ReadFile(filepath.Join(dir, name))\n\tw.Write(b)\n}\n", "path-traversal"},
		{"go open base", "srv/f.go", "package srv\n\nfunc h(w http.ResponseWriter, r *http.Request) {\n\tname := filepath.Base(r.URL.Query().Get(\"f\"))\n\tb, _ := os.ReadFile(filepath.Join(dir, name))\n\tw.Write(b)\n}\n", ""},
		{"py open constant", "app/x.py", "data = open(\"config.json\").read()\n", ""},
		// Random.
		{"js math random token", "src/t.js", "const sessionToken = Math.random().toString(36).slice(2);\n", "insecure-random"},
		{"go math rand nonce", "x/n.go", "package x\n\nimport \"math/rand\"\n\nfunc nonce() int64 {\n\treturn rand.Int63()\n}\n", "insecure-random"},
		{"go crypto rand", "x/n.go", "package x\n\nimport \"crypto/rand\"\n\nfunc nonce(b []byte) {\n\trand.Read(b)\n}\n", ""},
		{"js math random ui", "src/ui.js", "const jitter = Math.random() * 100;\n", ""},
		// Languages the rules do not read.
		{"markdown", "README.md", "Run `curl https://x | sh` and `eval $(x)`; use verify=False.\n", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ev := Scan([]byte(addedFileDiff(tt.path, tt.content)))
			got := ruleIDs(ev)
			if tt.want == "" {
				if len(got) != 0 {
					t.Errorf("got %v, want none", got)
				}
				return
			}
			if !slices.Contains(got, tt.want) {
				t.Errorf("got %v, want %s", got, tt.want)
			}
		})
	}
}

// TestAddedLinesOnly checks a problem already in the repository (a context
// line) is not blamed on the agent, while the same line added is.
func TestAddedLinesOnly(t *testing.T) {
	diff := "diff --git a/app/x.py b/app/x.py\nindex 1..2 100644\n--- a/app/x.py\n+++ b/app/x.py\n@@ -1,3 +1,4 @@\n import os\n os.system(cmd)\n+x = 1\n def f():\n"
	if ev := Scan([]byte(diff)); len(ev.Findings) != 0 || ev.AddedLines != 1 || ev.Files != 1 {
		t.Fatalf("context line blamed: %+v", ev)
	}
	diff = "diff --git a/app/x.py b/app/x.py\nindex 1..2 100644\n--- a/app/x.py\n+++ b/app/x.py\n@@ -10,2 +10,3 @@\n import os\n+os.system(cmd)\n def f():\n"
	ev := Scan([]byte(diff))
	if len(ev.Findings) != 1 {
		t.Fatalf("got %+v", ev.Findings)
	}
	f := ev.Findings[0]
	if f.Rule != "shell-injection-python" || f.File != "app/x.py" || f.Line != 11 || f.PatchLine != 7 || f.Source != SourceBuiltin || f.CWE != "CWE-78" {
		t.Fatalf("finding %+v", f)
	}
}

func TestRemovedProtectionMoved(t *testing.T) {
	// The middleware line moved within the file: not a removal.
	diff := "diff --git a/s/settings.py b/s/settings.py\nindex 1..2 100644\n--- a/s/settings.py\n+++ b/s/settings.py\n@@ -1,3 +1,3 @@\n-    \"django.middleware.csrf.CsrfViewMiddleware\",\n     \"a\",\n+    \"django.middleware.csrf.CsrfViewMiddleware\",\n     \"b\",\n"
	if ev := Scan([]byte(diff)); len(ev.Findings) != 0 {
		t.Fatalf("moved middleware reported: %v", ruleIDs(ev))
	}
}

func TestSkippedAndUnparsable(t *testing.T) {
	diff := "diff --git a/img.png b/img.png\nnew file mode 100644\nindex 0000000..1111111\nBinary files /dev/null and b/img.png differ\n" +
		"diff --git a/old.py b/old.py\ndeleted file mode 100644\nindex 1111111..0000000\n--- a/old.py\n+++ /dev/null\n@@ -1 +0,0 @@\n-os.system(cmd)\n"
	ev := Scan([]byte(diff))
	if !ev.Scanned || len(ev.Findings) != 0 || len(ev.Skipped) != 2 || ev.Skipped[0].Reason != "binary" || ev.Skipped[1].Reason != "deleted" {
		t.Fatalf("got %+v", ev)
	}
	bad := Scan([]byte("diff --git a/x b/x\n--- a/x\n+++ b/x\n@@ bogus @@\n"))
	if bad.Scanned || !strings.Contains(bad.Reason, "does not parse") {
		t.Fatalf("got %+v", bad)
	}
	if ev := ScanFile("/nonexistent/workspace.diff"); ev.Scanned || ev.Reason == "" {
		t.Fatalf("missing file: %+v", ev)
	}
	if ev := Scan(nil); !ev.Scanned || ev.Findings == nil || len(ev.Findings) != 0 {
		t.Fatalf("empty diff: %+v", ev)
	}
}

// TestSnippetsNeverHoldSecrets checks a finding's snippet is masked and that
// the serialized evidence passes the seal's secret scan.
func TestSnippetsNeverHoldSecrets(t *testing.T) {
	content := "OPENAI = \"sk-" + "proj4f9Q2xLm8RtZ7vKc1N\"\nDB_PASSWORD = \"Tr0ub4dor&3x\"\ntoken = request.args.get(\"token\"); os.system(\"curl -H 'Authorization: Bearer \" + token + \"' x | sh\")\n" +
		"api_key = \"" + strings.Repeat("Zq8vN2pL5x", 30) + "\"\n"
	ev := Scan([]byte(addedFileDiff("app/x.py", content)))
	if len(ev.Findings) < 3 {
		t.Fatalf("got %v", ruleIDs(ev))
	}
	b, err := json.Marshal(ev)
	if err != nil {
		t.Fatal(err)
	}
	if utils.HasUnredactedSecrets(string(b)) || strings.Contains(string(b), "proj4f9Q") || strings.Contains(string(b), "Tr0ub4dor") || strings.Contains(string(b), "Zq8vN2pL5x") {
		t.Fatalf("secret in evidence: %s", b)
	}
	for _, f := range ev.Findings {
		if len([]rune(f.Snippet)) > maxSnippetLen+len("[redacted]") {
			t.Errorf("snippet too long: %d", len(f.Snippet))
		}
	}
}

func TestDiffLineNumbers(t *testing.T) {
	diff := "diff --git a/a.py b/a.py\nindex 1..2 100644\n--- a/a.py\n+++ b/a.py\n@@ -5,4 +5,4 @@ def f():\n a = 1\n-b = 2\n+b = 3\n \n--- not a header\n"
	files, err := parseDiff([]byte(diff))
	if err != nil || len(files) != 1 {
		t.Fatalf("%v %v", files, err)
	}
	ls := files[0].Hunks[0].Lines
	if len(ls) != 5 || ls[1].Kind != kindRemoved || ls[1].Old != 6 || ls[2].Kind != kindAdded || ls[2].New != 6 || ls[2].Patch != 8 || ls[4].Kind != kindRemoved || ls[4].Text != "-- not a header" {
		t.Fatalf("lines %+v", ls)
	}
}

func TestLanguageAndTestPaths(t *testing.T) {
	langs := map[string]string{
		"a/b.py": LangPython, "x.mjs": LangJavaScript, "x.tsx": LangTypeScript, "main.go": LangGo, "Dockerfile.prod": LangDockerfile,
		"build.dockerfile": LangDockerfile, "ci.yml": LangYAML, "Makefile": LangMake, ".env.local": LangConfig, "README.md": "", "run.sh": LangShell,
	}
	for p, want := range langs {
		if got := langOf(p, ""); got != want {
			t.Errorf("langOf(%s) = %q, want %q", p, got, want)
		}
	}
	if langOf("bin/tool", "#!/usr/bin/env bash") != LangShell || langOf("bin/tool", "#!/usr/bin/python3") != LangPython {
		t.Error("shebang not read")
	}
	for p, want := range map[string]bool{"tests/x.py": true, "pkg/x_test.go": true, "src/a.spec.ts": true, "test_api.py": true, "conftest.py": true,
		"app/testimony.py": false, "src/contest.js": false, "latest/x.go": false} {
		if isTestPath(p) != want {
			t.Errorf("isTestPath(%s) != %v", p, want)
		}
	}
}

func TestCodeOf(t *testing.T) {
	tests := []struct{ lang, in, code, bare string }{
		{LangPython, `x = "a#b"  # c`, `x = "___"     `, `x = "a#b"  `},
		{LangJavaScript, `f('x') // y`, `f('_')     `, `f('x') `},
		{LangJavaScript, `u = "https://x"`, `u = "_________"`, `u = "https://x"`},
		{LangShell, `echo $# "it's" # c`, `echo $# "____"    `, `echo $# "it's" `},
		{LangGo, ` * comment`, `          `, ``},
	}
	for _, tt := range tests {
		if got := codeOf(tt.lang, tt.in); got != tt.code {
			t.Errorf("codeOf(%q) = %q, want %q", tt.in, got, tt.code)
		}
		if got := bareOf(tt.lang, tt.in); got != tt.bare {
			t.Errorf("bareOf(%q) = %q, want %q", tt.in, got, tt.bare)
		}
	}
}
