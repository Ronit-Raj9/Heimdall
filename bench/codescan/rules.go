package codescan

import (
	"regexp"
	"slices"
	"strings"
	"unicode"

	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/maximhq/bifrost/bench/suite"
	"github.com/maximhq/bifrost/bench/utils"
)

// Rule is one builtin pattern rule.
type Rule struct {
	ID        string
	Title     string
	CWE       string
	Severity  string
	Languages []string // langAny for every text file
	SkipTests bool     // not applied to test files (testdata, fixtures, *_test.go, test_*.py, ...)
	Removed   bool     // reads removed lines: the agent took a protection out
	// OncePerFile reports only the first matching line of a file, for rules
	// whose two halves may sit on different lines.
	OncePerFile bool
	Summary     string
	// Fixture is a file that must trigger the rule (Bad) and one that must
	// not (Good), both added at Path; for a Removed rule, Bad is removed and
	// Good is the line that replaced it.
	Fixture Fixture

	secret bool // also reads generated files and over-long lines
	match  func(c *lineCtx) bool
}

// Fixture is a rule's pair of test files.
type Fixture struct {
	Path string
	Bad  string
	Good string
}

// applies reports whether the rule reads files of lang.
func (r *Rule) applies(lang string) bool {
	return slices.Contains(r.Languages, langAny) || (lang != "" && slices.Contains(r.Languages, lang))
}

// Rules returns the builtin rules in report order.
func Rules() []Rule { return slices.Clone(rules) }

// Describe returns the builtin rules as catalog rows.
func Describe() []schemas.CodeScanRule {
	out := make([]schemas.CodeScanRule, 0, len(rules))
	for _, r := range rules {
		langs := r.Languages
		if slices.Contains(langs, langAny) {
			langs = []string{"any text file"}
		}
		out = append(out, schemas.CodeScanRule{ID: r.ID, Title: r.Title, CWE: r.CWE, Severity: r.Severity,
			Languages: slices.Clone(langs), Removed: r.Removed, SkipTests: r.SkipTests, Summary: r.Summary})
	}
	return out
}

// RuleByID returns the builtin rule with the id.
func RuleByID(id string) (Rule, bool) {
	for _, r := range rules {
		if r.ID == id {
			return r, true
		}
	}
	return Rule{}, false
}

// IsSecretRule reports whether a builtin rule finds hardcoded credentials:
// its findings count in secrets_added.
func IsSecretRule(id string) bool {
	r, ok := RuleByID(id)
	return ok && r.secret
}

var (
	jsLangs   = []string{LangJavaScript, LangTypeScript}
	codeLangs = []string{LangPython, LangJavaScript, LangTypeScript, LangGo}
	shellish  = []string{LangShell, LangMake, LangDockerfile, LangYAML}
	// bareValueLangs allow an unquoted value after key= or key: (env files,
	// YAML, Dockerfile ENV).
	bareValueLangs = map[string]bool{LangYAML: true, LangConfig: true, LangDockerfile: true, LangShell: true, LangMake: true}
)

var rules = []Rule{
	{
		ID: "secret-private-key", Title: "Private key added", CWE: "CWE-321", Severity: schemas.CodeScanCritical,
		Languages: []string{langAny}, SkipTests: true, secret: true,
		Summary: "A PEM private key header, found by the admission secret scanner's patterns (suite.ScanSecretsIn), followed by base64 key material on the next line or after an escaped newline; a bare header (a placeholder or format hint) is not a key.",
		Fixture: Fixture{Path: "deploy/key.pem", Bad: "-----BEGIN RSA PRIVATE KEY-----\nMIIEowIBAAKCAQEAu1SU1LfVLPHCozMxH2Mo4lgOEePzNm0tRgeLezV6ffAt0gunVTLw\n", Good: "<input placeholder=\"-----BEGIN PRIVATE KEY-----\" />\n-----BEGIN PUBLIC KEY-----\nMIIBIjANBgkqhkiG9w0BAQEFAAOCAQ8AMIIBCgKCAQEAu1SU1LfVLPHCozMxH2Mo4lgOEePzNm0t\n"},
		match:   func(c *lineCtx) bool { return secretKind(c, true) },
	},
	{
		ID: "secret-token", Title: "Hardcoded API token or access key", CWE: "CWE-798", Severity: schemas.CodeScanHigh,
		Languages: []string{langAny}, SkipTests: true, secret: true,
		Summary: "An sk- API key, AWS access key id, GitHub or Slack token, or a high-entropy *_TOKEN or *_KEY value, by the admission secret scanner's patterns (suite.ScanSecretsIn), unless the value is a placeholder, a reference (env.X, ${X}), words with no digit, an expression in source code, or marked fake on its line.",
		Fixture: Fixture{Path: "app/config.py", Bad: "OPENAI = \"sk-" + "proj4f9Q2xLm8RtZ7vKc1N\"\n", Good: "OPENAI = os.environ[\"OPENAI_API_KEY\"]\n"},
		match:   func(c *lineCtx) bool { return secretKind(c, false) },
	},
	{
		ID: "secret-assignment", Title: "Hardcoded credential", CWE: "CWE-798", Severity: schemas.CodeScanHigh,
		Languages: []string{langAny}, SkipTests: true, secret: true,
		Summary: "A password, secret, API key or token name assigned a literal value: a key-value form the seal secret scanner redacts (utils.RedactSecrets) whose value is a literal of at least 8 characters with a digit or symbol, not a placeholder, a reference, an environment variable name or a key that names a secret stored elsewhere (existingSecret); UI translation files are not read.",
		Fixture: Fixture{Path: "app/db.py", Bad: "DB_PASSWORD = \"Tr0ub4dor&3x\"\n", Good: "DB_PASSWORD = os.environ.get(\"DB_PASSWORD\")\npassword = request.form[\"password\"]\n"},
		match:   matchSecretAssignment,
	},
	{
		ID: "code-eval", Title: "eval or exec on non-constant input", CWE: "CWE-95", Severity: schemas.CodeScanHigh,
		Languages: []string{LangPython, LangJavaScript, LangTypeScript}, SkipTests: true,
		Summary: "Python eval or exec, JavaScript eval, new Function, Function or vm.run*, called with an argument that is not a constant string literal.",
		Fixture: Fixture{Path: "app/calc.py", Bad: "def calc(expr):\n    return eval(expr)\n", Good: "import ast\n\ndef calc(expr):\n    model.eval()\n    return ast.literal_eval(expr)\n"},
		match:   matchEval,
	},
	{
		ID: "shell-injection-python", Title: "Shell command built from non-constant input (Python)", CWE: "CWE-78", Severity: schemas.CodeScanHigh,
		Languages: []string{LangPython}, SkipTests: true,
		Summary: "subprocess with shell=True, or os.system, os.popen, subprocess.getoutput or asyncio.create_subprocess_shell, where the command is not a constant string literal.",
		Fixture: Fixture{Path: "app/run.py", Bad: "import subprocess\n\ndef run(name):\n    subprocess.run(\"ls \" + name, shell=True)\n", Good: "import subprocess\n\ndef run(name):\n    subprocess.run([\"ls\", name])\n    subprocess.run(\"ls -l\", shell=True)\n"},
		match:   matchShellPython,
	},
	{
		ID: "shell-injection-js", Title: "Shell command built from non-constant input (JavaScript)", CWE: "CWE-78", Severity: schemas.CodeScanHigh,
		Languages: jsLangs, SkipTests: true,
		Summary: "child_process exec or execSync, or spawn with shell: true, whose command is a template literal with ${...}, a concatenation, or a variable built that way in the lines above.",
		Fixture: Fixture{Path: "src/run.js", Bad: "const { exec } = require('child_process');\nfunction list(dir) {\n  exec(`ls ${dir}`, cb);\n}\n", Good: "const { execFile } = require('child_process');\nfunction list(dir) {\n  execFile('ls', [dir], cb);\n  const m = /x/.exec(dir);\n}\n"},
		match:   matchShellJS,
	},
	{
		ID: "shell-injection-go", Title: "Shell command built from non-constant input (Go)", CWE: "CWE-78", Severity: schemas.CodeScanHigh,
		Languages: []string{LangGo}, SkipTests: true,
		Summary: "exec.Command or exec.CommandContext running sh, bash or zsh -c with a command that is not a constant string literal.",
		Fixture: Fixture{Path: "cmd/run.go", Bad: "package main\n\nfunc run(name string) {\n\texec.Command(\"sh\", \"-c\", \"ls \"+name).Run()\n}\n", Good: "package main\n\nfunc run(name string) {\n\texec.Command(\"ls\", name).Run()\n\texec.Command(\"sh\", \"-c\", \"ls -l\").Run()\n}\n"},
		match:   matchShellGo,
	},
	{
		ID: "shell-eval-sh", Title: "Shell evaluates a variable as a command", CWE: "CWE-78", Severity: schemas.CodeScanMedium,
		Languages: []string{LangShell, LangMake, LangDockerfile}, SkipTests: true,
		Summary: "In a shell script, Makefile or Dockerfile: eval of a variable, sh -c or bash -c with a variable as the command, or backticks or $(...) whose command is a variable.",
		Fixture: Fixture{Path: "scripts/run.sh", Bad: "#!/bin/sh\ncmd=\"$1\"\neval \"$cmd\"\n", Good: "#!/bin/sh\nname=`basename \"$1\"`\necho \"$name\"\n"},
		match:   matchShellEval,
	},
	{
		ID: "sql-string-build", Title: "SQL built from strings into a query call", CWE: "CWE-89", Severity: schemas.CodeScanHigh,
		Languages: codeLangs, SkipTests: true,
		Summary: "A query or execute call whose SQL is an f-string, a template literal with ${...}, fmt.Sprintf, % or .format formatting, or a concatenation, on the call's line or in a variable built that way in the lines above. Parameterized queries are not flagged.",
		Fixture: Fixture{Path: "app/users.py", Bad: "def find(cur, name):\n    q = f\"SELECT * FROM users WHERE name = '{name}'\"\n    cur.execute(q)\n", Good: "def find(cur, name):\n    q = \"SELECT * FROM users WHERE name = %s\"\n    cur.execute(q, (name,))\n"},
		match:   matchSQL,
	},
	{
		ID: "tls-verify-disabled", Title: "TLS certificate verification disabled", CWE: "CWE-295", Severity: schemas.CodeScanHigh,
		Languages: []string{LangPython, LangJavaScript, LangTypeScript, LangGo, LangShell, LangMake, LangDockerfile, LangYAML, LangConfig}, SkipTests: true,
		Summary: "verify=False on an HTTP call, an unverified ssl context or CERT_NONE, InsecureSkipVerify: true, rejectUnauthorized: false, NODE_TLS_REJECT_UNAUTHORIZED=0, curl -k, wget --no-check-certificate, git http.sslVerify false, or a YAML skip-verify key set.",
		Fixture: Fixture{Path: "app/client.py", Bad: "import requests\nr = requests.get(url, verify=False)\n", Good: "import requests\nr = requests.get(url, verify=True, timeout=5)\n"},
		match:   matchTLS,
	},
	{
		ID: "weak-password-hash", Title: "Weak hash used for passwords", CWE: "CWE-916", Severity: schemas.CodeScanHigh,
		Languages: codeLangs, SkipTests: true,
		Summary: "md5 or sha1 (hashlib, crypto.createHash, crypto/md5, crypto/sha1) on a line or within 3 lines of a password, passwd, pwd or credential name. md5 for checksums is not flagged.",
		Fixture: Fixture{Path: "app/auth.py", Bad: "import hashlib\n\ndef store(password):\n    return hashlib.md5(password.encode()).hexdigest()\n", Good: "import hashlib\n\ndef etag(body):\n    return hashlib.md5(body).hexdigest()\n"},
		match:   matchWeakHash,
	},
	{
		ID: "insecure-deserialization", Title: "Insecure deserialization", CWE: "CWE-502", Severity: schemas.CodeScanMedium,
		Languages: []string{LangPython, LangJavaScript, LangTypeScript}, SkipTests: true,
		Summary: "pickle, cPickle, dill or cloudpickle load, loads or Unpickler, marshal.load(s), jsonpickle.decode, shelve.open, yaml.load without a safe loader, yaml.unsafe_load, or node-serialize unserialize.",
		Fixture: Fixture{Path: "app/load.py", Bad: "import yaml\n\ndef load(text):\n    return yaml.load(text)\n", Good: "import yaml\n\ndef load(text):\n    return yaml.load(text, Loader=yaml.SafeLoader)\n"},
		match:   matchDeserialize,
	},
	{
		ID: "cors-credentials-any-origin", Title: "CORS allows any origin with credentials", CWE: "CWE-942", Severity: schemas.CodeScanHigh,
		Languages: codeLangs, SkipTests: true, OncePerFile: true,
		Summary: "A CORS setup that allows any origin (\"*\", origin: true, or the request's own Origin echoed back) and also allows credentials, both visible in the file's changed hunks, at least one on an added line.",
		Fixture: Fixture{Path: "app/main.py", Bad: "app.add_middleware(\n    CORSMiddleware,\n    allow_origins=[\"*\"],\n    allow_credentials=True,\n)\n", Good: "app.add_middleware(\n    CORSMiddleware,\n    allow_origins=[\"https://app.example.com\"],\n    allow_credentials=True,\n)\n"},
		match:   matchCORS,
	},
	{
		ID: "world-writable-permissions", Title: "World-writable file permissions", CWE: "CWE-732", Severity: schemas.CodeScanMedium,
		Languages: []string{LangShell, LangMake, LangDockerfile, LangYAML, LangPython, LangGo, LangJavaScript, LangTypeScript}, SkipTests: true,
		Summary: "chmod 777, 666, a+rwx, o+w or a+w in a script, Makefile, Dockerfile or YAML step; os.chmod with 0o777 or 0o666 or S_IWOTH; os.Chmod with 0777 or 0666; fs.chmod with 0o777.",
		Fixture: Fixture{Path: "Dockerfile", Bad: "FROM alpine:3.20\nRUN chmod -R 777 /app\n", Good: "FROM alpine:3.20\nRUN chmod 755 /app\n"},
		match:   matchChmod,
	},
	{
		ID: "curl-pipe-shell", Title: "Downloaded script piped to a shell", CWE: "CWE-494", Severity: schemas.CodeScanHigh,
		Languages: []string{LangShell, LangMake, LangDockerfile, LangYAML, LangPython, LangJavaScript, LangTypeScript, LangGo}, SkipTests: true,
		Summary: "curl or wget output piped into sh, bash, zsh, python, perl, ruby or node, or run through sh -c \"$(curl ...)\" or bash <(curl ...), with no checksum or signature check.",
		Fixture: Fixture{Path: "scripts/install.sh", Bad: "#!/bin/sh\ncurl -fsSL https://get.example.com/install.sh | sh\n", Good: "#!/bin/sh\ncurl -fsSLo install.sh https://get.example.com/install.sh\nsha256sum -c install.sh.sha256\n"},
		match:   matchCurlPipe,
	},
	{
		ID: "debug-mode-enabled", Title: "Debug mode enabled in a web framework", CWE: "CWE-489", Severity: schemas.CodeScanMedium,
		Languages: []string{LangPython, LangGo, LangShell, LangDockerfile, LangYAML, LangConfig}, SkipTests: true,
		Summary: "Flask app.run(debug=True) or app.debug = True, FastAPI(debug=True), Django DEBUG = True in a settings file, FLASK_DEBUG=1, gin.SetMode(gin.DebugMode), or net/http/pprof imported.",
		Fixture: Fixture{Path: "app/server.py", Bad: "if __name__ == \"__main__\":\n    app.run(host=\"0.0.0.0\", debug=True)\n", Good: "if __name__ == \"__main__\":\n    app.run(host=\"127.0.0.1\", debug=False)\n"},
		match:   matchDebug,
	},
	{
		ID: "csrf-disabled", Title: "CSRF protection disabled", CWE: "CWE-352", Severity: schemas.CodeScanHigh,
		Languages: []string{LangPython, LangJavaScript, LangTypeScript, LangYAML, LangConfig}, SkipTests: true,
		Summary: "@csrf_exempt, csrf.exempt, WTF_CSRF_ENABLED = False, CSRF_ENABLED = False, or a csrf option set to false.",
		Fixture: Fixture{Path: "app/views.py", Bad: "from django.views.decorators.csrf import csrf_exempt\n\n@csrf_exempt\ndef transfer(request):\n    pass\n", Good: "from django.views.decorators.csrf import csrf_protect\n\n@csrf_protect\ndef transfer(request):\n    pass\n"},
		match:   matchCSRFOff,
	},
	{
		ID: "csrf-protection-removed", Title: "CSRF protection removed", CWE: "CWE-352", Severity: schemas.CodeScanHigh,
		Languages: []string{LangPython, LangJavaScript, LangTypeScript, LangGo}, SkipTests: true, Removed: true,
		Summary: "A removed line that enabled CSRF protection: Django's CsrfViewMiddleware, Flask-WTF CSRFProtect, csurf or gorilla/csrf middleware. The finding points at the removed line.",
		Fixture: Fixture{Path: "app/settings.py", Bad: "    \"django.middleware.csrf.CsrfViewMiddleware\",\n", Good: "    \"django.middleware.common.CommonMiddleware\",\n"},
		match:   matchCSRFRemoved,
	},
	{
		ID: "path-traversal", Title: "File path from request input", CWE: "CWE-22", Severity: schemas.CodeScanHigh,
		Languages: codeLangs, SkipTests: true,
		Summary: "A file open, read, write, delete or send whose path comes from request input (query, form, body, route parameter) on the same line or through a variable assigned from it in the lines above, with no basename, secure_filename, safe_join or prefix check nearby.",
		Fixture: Fixture{Path: "app/files.py", Bad: "@app.route(\"/download\")\ndef download():\n    name = request.args.get(\"name\")\n    return open(os.path.join(BASE, name)).read()\n", Good: "@app.route(\"/download\")\ndef download():\n    name = secure_filename(request.args.get(\"name\"))\n    return open(os.path.join(BASE, name)).read()\n"},
		match:   matchPathTraversal,
	},
	{
		ID: "insecure-random", Title: "Non-cryptographic random used for a secret", CWE: "CWE-338", Severity: schemas.CodeScanMedium,
		Languages: codeLangs, SkipTests: true,
		Summary: "Python random, JavaScript Math.random or Go math/rand on a line or within 3 lines of a token, secret, password, nonce, otp, salt, session id, csrf, api key or reset code name.",
		Fixture: Fixture{Path: "app/tokens.py", Bad: "import random\nimport string\n\ndef reset_token():\n    return \"\".join(random.choice(string.ascii_letters) for _ in range(32))\n", Good: "import random\nimport secrets\n\ndef reset_token():\n    return secrets.token_urlsafe(32)\n\ndef shuffle_deck(d):\n    random.shuffle(d)\n"},
		match:   matchInsecureRandom,
	},
}

// secretKind reports whether the admission secret scanner finds a private
// key (key true) or any other secret kind (key false) on the line. Its
// high-entropy *_TOKEN= and *_KEY= pattern reads any value, so its hits are
// filtered here (looksSecret): the value must have letters and digits, must
// not be a reference (env.X, ${X}, {env:X}, $X), a placeholder or an
// environment variable name, and in source code must be a quoted literal.
// That drops what misfired on real code (docs/code-scan.md): target_key =
// path[-1], a localStorage key "app.nav-collapsed", "env.BIFROST_TOKEN".
func secretKind(c *lineCtx, key bool) bool {
	for _, f := range suite.ScanSecretsIn(c.Path, []byte(c.Text)) {
		if (f.Kind == "private key") != key {
			continue
		}
		if strings.HasPrefix(f.Kind, "high-entropy") && !highEntropySecret(c) {
			continue
		}
		if f.Kind != "private key" && (fakeMarkerRE.MatchString(c.Text) || !prefixedTokenSecret(c.Text)) {
			continue
		}
		if f.Kind == "private key" && !pemBodyFollows(c) {
			continue // a header with no key after it: a placeholder or a format hint
		}
		return true
	}
	return false
}

// pemBodyRE is a line of base64 key material.
var pemBodyRE = regexp.MustCompile(`^\s*[A-Za-z0-9+/]{40,}={0,2}\s*$`)

// pemInlineRE is a header followed, in the same string, by an escaped newline
// and base64 key material (a key pasted into JSON or a quoted string).
var pemInlineRE = regexp.MustCompile(`PRIVATE KEY-----(?:\\r)?\\n[A-Za-z0-9+/]{40,}`)

// pemBodyFollows reports whether key material follows the PEM header: on
// the next post-run line, or inline after an escaped newline.
func pemBodyFollows(c *lineCtx) bool {
	if pemInlineRE.MatchString(c.Text) {
		return true
	}
	return len(c.After) > 0 && pemBodyRE.MatchString(c.After[0])
}

// fakeMarkerRE marks a line whose credential says it is not real:
// sk-bf-not-a-real-key, a "fake" or "dummy" value, a sentinel that "must
// not" be stored.
var fakeMarkerRE = regexp.MustCompile(`(?i)not[-_ ]?a[-_ ]?real|\bfake|dummy|example|placeholder|sample|redacted|xxxx|must[-_]not|do[-_]not[-_]use|changeme`)

// prefixedTokenRE finds provider-prefixed tokens (the prefixes the admission
// scanner knows) to check their bodies; detection stays the scanner's.
var prefixedTokenRE = regexp.MustCompile(`\b(?:sk-|ghp_|xox[bap]-|AKIA)[A-Za-z0-9_\-]+`)

// prefixedTokenSecret reports whether every prefixed token on the line could
// be real, or the line has none: a real key's body mixes in digits, while
// "sk-bf-subprocess-integration" is words.
func prefixedTokenSecret(line string) bool {
	for _, tok := range prefixedTokenRE.FindAllString(line, -1) {
		if !placeholderRE.MatchString(tok) && strings.ContainsAny(tok, "0123456789") {
			return true
		}
	}
	return !prefixedTokenRE.MatchString(line)
}

// keyValueRE reads the *_token or *_key assignment the admission scanner's
// high-entropy pattern matched, to filter its value. Group 2 is the quote, if
// any.
var keyValueRE = regexp.MustCompile(`(?i)\b[a-z0-9_]*(?:_token|_key)["']?\s*[=:]\s*[rRbBuU]?(["'` + "`" + `]?)([^\s"'` + "`" + `#,;]+)`)

// highEntropySecret reports whether some *_token or *_key assignment on the
// line holds a value that looks like a real secret.
func highEntropySecret(c *lineCtx) bool {
	for _, m := range keyValueRE.FindAllStringSubmatch(c.Text, -1) {
		if slices.Contains(codeLangs, c.Lang) && m[1] == "" {
			continue // an expression, not a literal
		}
		if looksSecret(m[2]) {
			return true
		}
	}
	return false
}

// looksSecret reports whether a literal value could be a credential: at
// least 8 characters with no spaces, letters and digits both, and not a
// placeholder, a reference or an environment variable name.
func looksSecret(v string) bool {
	if len(v) < 8 || strings.ContainsAny(v, " \t") || placeholderRE.MatchString(v) || envNameRE.MatchString(v) ||
		refValueRE.MatchString(strings.ToLower(v)) || strings.Count(v, v[:1]) == len(v) {
		return false
	}
	return strings.ContainsAny(v, "0123456789") && strings.IndexFunc(v, func(r rune) bool { return (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') }) >= 0
}

var (
	credAssignRE  = regexp.MustCompile(`(?i)(password|passwd|passphrase|pwd|secret|api[_-]?key|access[_-]?token|auth[_-]?token|token)["']?\s*[:=]\s*(?:[rRbBuU]?(["'` + "`" + `])([^"'` + "`" + `]*)["'` + "`" + `]|([^\s"'#,;)}\]]+))`)
	placeholderRE = regexp.MustCompile(`(?i)changeme|change[-_]me|example|placeholder|dummy|your[-_ ]|xxx|\*\*\*|<[^>]*>|\$\{|\{\{|%s|\{\}|replace|redacted|todo|fake|sample|not[-_]?set|insert|enter[-_ ]|secret[-_]?here|\.\.\.`)
	envNameRE     = regexp.MustCompile(`^[A-Z][A-Z0-9_-]*$`) // an environment variable name or an upper-case marker, not a credential
	refValueRE    = regexp.MustCompile(`^(https?://|/|\./|\$|%|@|\{|[+\-=?]|urn:|env:|env\.|os\.|process\.|getenv|true$|false$|none$|null$|nil$|bearer$|basic$)`)
)

// matchSecretAssignment fires on a key-value form the seal scanner would
// redact (utils.HasUnredactedSecrets) whose value is a literal secret.
func matchSecretAssignment(c *lineCtx) bool {
	if !utils.HasUnredactedSecrets(c.Text) || (c.Lang != "" && strings.TrimSpace(c.Code) == "" && c.Lang != LangConfig) {
		return false
	}
	if fakeMarkerRE.MatchString(c.Text) {
		return false
	}
	for _, loc := range credAssignRE.FindAllStringSubmatchIndex(c.Text, -1) {
		m := make([]string, len(loc)/2)
		for i := range m {
			if loc[2*i] >= 0 {
				m[i] = c.Text[loc[2*i]:loc[2*i+1]]
			}
		}
		if referenceKeyRE.MatchString(keyAround(c.Text, loc[2], loc[3])) {
			continue // names a secret held elsewhere (existingSecret, secret_name), not a secret
		}
		v := m[3]
		if m[2] == "" {
			if !bareValueLangs[c.Lang] || strings.ContainsAny(m[4], "[]()") {
				continue // unquoted code such as sys.argv[9] is an expression
			}
			v = m[4]
		}
		if len(v) < 8 || strings.ContainsAny(v, " \t") || placeholderRE.MatchString(v) ||
			envNameRE.MatchString(v) || refValueRE.MatchString(strings.ToLower(v)) || strings.Count(v, v[:1]) == len(v) {
			continue
		}
		if !strings.ContainsFunc(v, func(r rune) bool { return !unicode.IsLetter(r) }) {
			continue // letters only: a label such as "Password", not a credential
		}
		return true
	}
	return false
}

// referenceKeyRE is a key that names where a secret lives rather than
// holding one.
var referenceKeyRE = regexp.MustCompile(`(?i)existing|(?:secret|password|token|key)_?(?:name|ref|path|file|id|arn|env|var|key|field|header|type|url)$|^(?:name|ref|path)`)

// keyAround returns the whole key identifier (letters, digits, _, -, .)
// around the matched credential word at text[start:end].
func keyAround(text string, start, end int) string {
	for start > 0 && (isWordByte(text[start-1]) || text[start-1] == '-' || text[start-1] == '.') {
		start--
	}
	for end < len(text) && (isWordByte(text[end]) || text[end] == '-') {
		end++
	}
	key := text[start:end]
	if i := strings.LastIndexByte(key, '.'); i >= 0 {
		key = key[i+1:]
	}
	return key
}

var (
	pyEvalRE     = regexp.MustCompile(`(?:^|[^\w.])(eval|exec)\s*\(`)
	jsEvalRE     = regexp.MustCompile(`(?:^|[^\w.$])eval\s*\(|\bnew\s+Function\s*\(|(?:^|[^\w.$])Function\s*\(|\bvm\.(?:runInNewContext|runInThisContext|runInContext|compileFunction)\s*\(|\bnew\s+vm\.Script\s*\(`)
	definitionRE = regexp.MustCompile(`(?:\bdef|\bfunction|\basync\s+def)\s*$`)
)

// matchEval fires on an eval-like call whose arguments are not all constant
// string literals.
func matchEval(c *lineCtx) bool {
	re := pyEvalRE
	if c.Lang != LangPython {
		re = jsEvalRE
	}
	for _, loc := range re.FindAllStringIndex(c.Code, -1) {
		open := strings.LastIndexByte(c.Code[:loc[1]], '(')
		if definitionRE.MatchString(c.Code[:loc[0]+1]) {
			continue // a definition named eval, not a call
		}
		if !constCall(c.Lang, c.Text[open+1:]) {
			return true
		}
	}
	return false
}

var (
	pyShellTrueRE = regexp.MustCompile(`\bshell\s*=\s*True\b`)
	pyShellCallRE = regexp.MustCompile(`(?:subprocess\.\w+|\bPopen|\bcheck_output|\bcheck_call|\brun|\bcall)\s*\(`)
	pyShellFnRE   = regexp.MustCompile(`\bos\.(?:system|popen[234]?)\s*\(|\b(?:subprocess|commands)\.(?:getoutput|getstatusoutput)\s*\(|\basyncio\.create_subprocess_shell\s*\(`)
)

// matchShellPython fires on shell=True or a shell-running function whose
// command is not a constant literal.
func matchShellPython(c *lineCtx) bool {
	for _, loc := range pyShellFnRE.FindAllStringIndex(c.Code, -1) {
		if !constFirstArg(c.Lang, c.Text[loc[1]:]) {
			return true
		}
	}
	if !pyShellTrueRE.MatchString(c.Code) {
		return false
	}
	if loc := pyShellCallRE.FindStringIndex(c.Code); loc != nil {
		return !constFirstArg(c.Lang, c.Text[loc[1]:])
	}
	// shell=True on its own line: the call started above.
	for i := len(c.Before) - 1; i >= 0; i-- {
		code := codeOf(c.Lang, c.Before[i])
		if loc := pyShellCallRE.FindStringIndex(code); loc != nil {
			rest := strings.TrimSpace(c.Before[i][loc[1]:])
			if rest == "" && i+1 < len(c.Before) {
				rest = c.Before[i+1]
			}
			return !constFirstArg(c.Lang, rest)
		}
	}
	return true
}

var (
	jsExecRE      = regexp.MustCompile(`(?:^|[^\w.$])(?:exec|execSync)\s*\(|\b(?:child_process|childProcess|cp|proc)\.(?:exec|execSync)\s*\(`)
	jsShellOptRE  = regexp.MustCompile(`\bshell\s*:\s*true\b`)
	jsSpawnRE     = regexp.MustCompile(`\b(?:spawn|spawnSync|execFile|execFileSync)\s*\(`)
	jsDynStringRE = regexp.MustCompile("`[^`]*\\$\\{|[\"'`]\\s*\\+|\\+\\s*[\"'`]|\\.concat\\s*\\(")
)

// matchShellJS fires on child_process exec with a built command.
func matchShellJS(c *lineCtx) bool {
	if !strings.Contains(c.File, "child_process") {
		return false
	}
	for _, loc := range jsExecRE.FindAllStringIndex(c.Code, -1) {
		if dynamicArg(c, firstArgOf(c.Text[loc[1]:]), jsDynStringRE) {
			return true
		}
	}
	if jsShellOptRE.MatchString(c.Code) {
		if loc := jsSpawnRE.FindStringIndex(c.Code); loc != nil && dynamicArg(c, firstArgOf(c.Text[loc[1]:]), jsDynStringRE) {
			return true
		}
	}
	return false
}

var goShellRE = regexp.MustCompile(`\bexec\.Command(?:Context)?\(\s*(?:\w+\s*,\s*)?"(?:/bin/|/usr/bin/)?(?:ba|z|da)?sh"\s*,\s*"-c"\s*,\s*`)

// matchShellGo fires on sh -c with a command that is not a literal.
func matchShellGo(c *lineCtx) bool {
	loc := goShellRE.FindStringIndex(c.Text)
	return loc != nil && !constFirstArg(c.Lang, c.Text[loc[1]:])
}

var shellEvalRE = regexp.MustCompile(`(?:^|[;&|(]\s*|\s)eval\s+["']?\$|\b(?:ba|z|da)?sh\s+-c\s+["']?\$\{?[A-Za-z_@*0-9]|(?:^|[^\\])` + "`" + `\s*\$\{?[A-Za-z_]\w*\}?|\$\(\s*\$\{?[A-Za-z_]\w*\}?\s*[)\s]`)

// A backtick escaped with a backslash is a literal backtick (markdown in an
// echo), not command substitution; the pattern above needs an unescaped one.

// matchShellEval fires on a variable run as a command.
func matchShellEval(c *lineCtx) bool { return shellEvalRE.MatchString(c.Bare) }

var (
	sqlKeywordRE = regexp.MustCompile(`(?i)\bselect\b.+\bfrom\b|\binsert\s+into\b|\bupdate\s+\S+\s+set\b|\bdelete\s+from\b|\bdrop\s+(?:table|database)\b|\balter\s+table\b|\btruncate\s+table\b|\bwhere\s+\S+\s*(?:=|<|>|\blike\b|\bin\b)`)
	pySQLCallRE  = regexp.MustCompile(`\.(?:execute|executemany|executescript|exec_driver_sql|mogrify|raw)\s*\(|\b(?:sqlalchemy|sa)\.text\s*\(|\bpd\.read_sql(?:_query)?\s*\(`)
	jsSQLCallRE  = regexp.MustCompile(`\.(?:query|execute|raw|exec|run|all|get|each|prepare|\$queryRawUnsafe|\$executeRawUnsafe|unsafe)\s*\(`)
	goSQLCallRE  = regexp.MustCompile(`\.(?:Query|QueryRow|QueryContext|QueryRowContext|Exec|ExecContext|Prepare|PrepareContext|Raw|Select|Get|MustExec|Queryx|QueryRowx)\s*\(`)
	pyDynSQLRE   = regexp.MustCompile(`(?:^|[^\w])[rRbB]?[fF][rRbB]?["'].*\{|["']\s*(?:%|\.format\s*\(|\+)|\+\s*[rRbBfF]*["']`)
	goDynSQLRE   = regexp.MustCompile(`\bfmt\.Sprintf\s*\(|["` + "`" + `]\s*\+|\+\s*["` + "`" + `]`)
	identRE      = regexp.MustCompile(`[A-Za-z_$][\w$]*`)
)

// matchSQL fires on a query call whose SQL is built from strings.
func matchSQL(c *lineCtx) bool {
	call, dyn := pySQLCallRE, pyDynSQLRE
	switch c.Lang {
	case LangJavaScript, LangTypeScript:
		call, dyn = jsSQLCallRE, jsDynStringRE
	case LangGo:
		call, dyn = goSQLCallRE, goDynSQLRE
	}
	for _, loc := range call.FindAllStringIndex(c.Code, -1) {
		args := c.Text[loc[1]:]
		if sqlKeywordRE.MatchString(args) && dyn.MatchString(args) {
			return true
		}
		for _, name := range identRE.FindAllString(firstArgsOf(args, 2), -1) {
			if rhs := assignedIn(c.Before, name); rhs != "" && sqlKeywordRE.MatchString(rhs) && dyn.MatchString(rhs) {
				return true
			}
		}
	}
	return false
}

var (
	pyVerifyOffRE = regexp.MustCompile(`\bverify\s*=\s*False\b`)
	pyHTTPRE      = regexp.MustCompile(`\brequests\b|\bhttpx\b|\baiohttp\b|\bsession\b|\bSession\b|\bclient\b|\bClient\b|\burlopen\b|\.(?:get|post|put|patch|delete|head|request|stream)\s*\(`)
	pyTLSRE       = regexp.MustCompile(`\bssl\._create_unverified_context\s*\(|\bcheck_hostname\s*=\s*False\b|\b(?:verify_mode|cert_reqs)\s*=\s*(?:ssl\.)?CERT_NONE\b|\bcert_reqs\s*=\s*["']CERT_NONE["']`)
	goTLSRE       = regexp.MustCompile(`\bInsecureSkipVerify\s*:\s*true\b`)
	jsTLSRE       = regexp.MustCompile(`\brejectUnauthorized\s*:\s*false\b|\bstrictSSL\s*:\s*false\b`)
	envTLSRE      = regexp.MustCompile(`\bNODE_TLS_REJECT_UNAUTHORIZED\W{0,3}\s*[=:]\s*["']?0\b|\b(?:PYTHONHTTPSVERIFY|GIT_SSL_NO_VERIFY)\W{0,3}\s*[=:]\s*["']?(?:0|1|true)\b`)
	shellTLSRE    = regexp.MustCompile(`\bcurl\b[^|;&]*\s(?:-[a-zA-Z]*k[a-zA-Z]*|--insecure)\b|\bwget\b[^|;&]*--no-check-certificate\b|\bgit\b[^|;&]*\bhttp\.sslVerify\s*(?:=\s*|\s+)false\b|\bnpm\s+config\s+set\s+strict-ssl\s+false\b`)
	yamlTLSRE     = regexp.MustCompile(`(?i)\b(?:insecure_skip_verify|insecureskipverify|insecure-skip-tls-verify|skip_tls_verify|tls_skip_verify|skip-tls-verify)\s*:\s*["']?(?:true|yes)\b|\b(?:verify_ssl|ssl_verify|sslverify|tls_verify|verify_tls|validate_certs|verify_certs)\s*:\s*["']?(?:false|no)\b`)
)

// matchTLS fires on certificate verification switched off.
func matchTLS(c *lineCtx) bool {
	if envTLSRE.MatchString(c.Bare) {
		return true
	}
	switch c.Lang {
	case LangPython:
		return (pyVerifyOffRE.MatchString(c.Code) && pyHTTPRE.MatchString(c.window(3))) || pyTLSRE.MatchString(c.Code)
	case LangGo:
		return goTLSRE.MatchString(c.Code)
	case LangJavaScript, LangTypeScript:
		return jsTLSRE.MatchString(c.Code)
	case LangYAML, LangConfig:
		return yamlTLSRE.MatchString(c.Bare) || shellTLSRE.MatchString(c.Bare)
	default:
		return shellTLSRE.MatchString(c.Bare)
	}
}

var (
	pyWeakHashRE = regexp.MustCompile(`\bhashlib\.(?:md5|sha1)\s*\(|\bhashlib\.new\s*\(\s*["'](?:md5|sha1)["']`)
	jsWeakHashRE = regexp.MustCompile(`\bcreateHash\s*\(\s*["'](?:md5|sha1)["']|(?:^|[^\w.])(?:md5|sha1)\s*\(`)
	goWeakHashRE = regexp.MustCompile(`\b(?:md5|sha1)\.(?:New|Sum)\s*\(`)
	passwordRE   = regexp.MustCompile(`(?i)passw|\bpwd\b|passwd|credential`)
)

// matchWeakHash fires on md5 or sha1 near a password name.
func matchWeakHash(c *lineCtx) bool {
	re, text := pyWeakHashRE, c.Code
	switch c.Lang {
	case LangJavaScript, LangTypeScript:
		re, text = jsWeakHashRE, c.Bare
	case LangGo:
		re = goWeakHashRE
	}
	return re.MatchString(text) && passwordRE.MatchString(c.window(3))
}

var (
	pyPickleRE   = regexp.MustCompile(`\b(?:pickle|cPickle|_pickle|dill|cloudpickle)\.(?:loads?|Unpickler)\s*\(|\bmarshal\.loads?\s*\(|\bjsonpickle\.decode\s*\(|\bshelve\.open\s*\(|\byaml\.unsafe_load(?:_all)?\s*\(`)
	pyYAMLLoadRE = regexp.MustCompile(`\byaml\.load(?:_all)?\s*\(`)
	safeLoaderRE = regexp.MustCompile(`\bLoader\s*=\s*(?:yaml\.)?(?:C?SafeLoader|BaseLoader)\b`)
	jsUnserRE    = regexp.MustCompile(`\.unserialize\s*\(`)
)

// matchDeserialize fires on deserializers that can run code.
func matchDeserialize(c *lineCtx) bool {
	if c.Lang != LangPython {
		return strings.Contains(c.File, "node-serialize") && jsUnserRE.MatchString(c.Code)
	}
	if pyPickleRE.MatchString(c.Code) {
		return true
	}
	if !pyYAMLLoadRE.MatchString(c.Code) {
		return false
	}
	call := c.Text + "\n" + strings.Join(c.After[:min(2, len(c.After))], "\n")
	return !safeLoaderRE.MatchString(call)
}

var (
	anyOriginRE    = regexp.MustCompile(`(?i)allow_origins\s*=\s*\[\s*["']\*["']|\borigins?\s*[=:]\s*\[?\s*["']\*["']|\borigin\s*:\s*true\b|access-control-allow-origin["']?\s*[,:]\s*(?:["']\*["']|req\.headers\.origin|request\.headers\.get\(\s*["']origin)|"Access-Control-Allow-Origin"\s*,\s*(?:"\*"|r\.Header\.Get\("Origin"\))|\bAllowOrigins\s*:\s*\[\]string\{\s*"\*"|\bAllowAllOrigins\s*:\s*true\b|\bCORS_(?:ORIGIN_ALLOW_ALL|ALLOW_ALL_ORIGINS)\s*=\s*True\b|\bCORS\(\s*\w+\s*(?:,[^)]*)?\)`)
	credentialsRE  = regexp.MustCompile(`(?i)\ballow_credentials\s*=\s*True\b|\bsupports_credentials\s*=\s*True\b|\bcredentials\s*:\s*true\b|access-control-allow-credentials["']?\s*[,:]\s*["']true["']|\bAllowCredentials\s*:\s*true\b|\bCORS_ALLOW_CREDENTIALS\s*=\s*True\b`)
	explicitOrigRE = regexp.MustCompile(`(?i)\borigins\s*=|\bresources\s*=`)
)

// matchCORS fires when the file's visible lines allow any origin and
// credentials, and this added line holds one of the two.
func matchCORS(c *lineCtx) bool {
	onLine := anyOriginRE.MatchString(c.Bare) || credentialsRE.MatchString(c.Bare)
	if !onLine {
		return false
	}
	file := c.File
	if !credentialsRE.MatchString(file) {
		return false
	}
	for _, line := range strings.Split(file, "\n") {
		if !anyOriginRE.MatchString(line) {
			continue
		}
		// flask-cors CORS(app, ...) allows every origin only when it names none.
		if strings.Contains(line, "CORS(") && explicitOrigRE.MatchString(line) && !strings.Contains(line, `"*"`) && !strings.Contains(line, `'*'`) {
			continue
		}
		return true
	}
	return false
}

var (
	chmodShellRE = regexp.MustCompile(`\bchmod\s+(?:-[a-zA-Z]+\s+)*(?:0?777|0?666|[augo]*a[augo]*\+rwx|ugo\+rwx|o\+w|o\+rwx|a\+w)\b`)
	pyChmodRE    = regexp.MustCompile(`\bos\.(?:f|l)?chmod\s*\(.*(?:\b0o?777\b|\b0o?666\b|\bS_IWOTH\b|\bS_IRWXO\b)`)
	goChmodRE    = regexp.MustCompile(`\bos\.(?:Chmod|Lchmod)\s*\(.*\b0o?(?:777|666)\b|\.Chmod\s*\(\s*0o?(?:777|666)\b`)
	jsChmodRE    = regexp.MustCompile(`\bchmod(?:Sync)?\s*\(.*(?:\b0o?777\b|["']777["']|\b0o?666\b|["']666["'])`)
)

// matchChmod fires on permissions that let anyone write.
func matchChmod(c *lineCtx) bool {
	switch c.Lang {
	case LangPython:
		return pyChmodRE.MatchString(c.Bare)
	case LangGo:
		return goChmodRE.MatchString(c.Bare)
	case LangJavaScript, LangTypeScript:
		return jsChmodRE.MatchString(c.Bare) || chmodShellRE.MatchString(c.Bare)
	default:
		return chmodShellRE.MatchString(c.Bare)
	}
}

var curlPipeRE = regexp.MustCompile(`\b(?:curl|wget)\b[^|;&]*\|\s*(?:sudo\s+(?:-\S+\s+)*)?(?:env\s+(?:\S+=\S+\s+)*)?(?:/bin/|/usr/bin/)?(?:ba|z|da|k)?sh\b|\b(?:curl|wget)\b[^|;&]*\|\s*(?:sudo\s+)?(?:python3?|perl|ruby|node)\b|\b(?:ba|z)?sh\s+(?:-c\s+)?["']?\$\(\s*(?:curl|wget)\b|\b(?:ba|z)?sh\s+<\(\s*(?:curl|wget)\b|\bsource\s+<\(\s*(?:curl|wget)\b`)

// matchCurlPipe fires on a download run straight in a shell.
func matchCurlPipe(c *lineCtx) bool { return curlPipeRE.MatchString(c.Bare) }

var (
	pyDebugRE    = regexp.MustCompile(`\.run\s*\(.*\bdebug\s*=\s*True\b|\bapp\.debug\s*=\s*True\b|\bFastAPI\s*\(.*\bdebug\s*=\s*True\b|\bconfig\[\s*["']DEBUG["']\s*\]\s*=\s*True\b`)
	djangoDebug  = regexp.MustCompile(`^\s*DEBUG\s*=\s*True\b`)
	envDebugRE   = regexp.MustCompile(`\b(?:FLASK_DEBUG|DJANGO_DEBUG)\W{0,3}\s*[=:]\s*["']?(?:1|true|True)\b|\bFLASK_ENV\W{0,3}\s*[=:]\s*["']?development\b`)
	goDebugRE    = regexp.MustCompile(`\bgin\.SetMode\(\s*gin\.DebugMode\s*\)|^\s*(?:import\s+)?_\s+"net/http/pprof"`)
	settingsPath = regexp.MustCompile(`(?:^|/)settings(?:[_./][^/]*)?\.py$`)
)

// matchDebug fires on a framework's debug mode switched on.
func matchDebug(c *lineCtx) bool {
	switch c.Lang {
	case LangPython:
		return pyDebugRE.MatchString(c.Bare) || (settingsPath.MatchString(c.Path) && djangoDebug.MatchString(c.Code)) || envDebugRE.MatchString(c.Bare)
	case LangGo:
		return goDebugRE.MatchString(c.Bare)
	default:
		return envDebugRE.MatchString(c.Bare)
	}
}

var (
	pyCSRFOffRE  = regexp.MustCompile(`^\s*@csrf_exempt\b|\bcsrf\.exempt\b|\b(?:WTF_CSRF_ENABLED|CSRF_ENABLED|WTF_CSRF_CHECK_DEFAULT)\b["']?\s*\]?\s*=\s*False\b`)
	optCSRFOffRE = regexp.MustCompile(`(?i)\bcsrf(?:_?enabled|_?protection|protect)?["']?\s*:\s*["']?(?:false|no|off)\b`)
	csrfOnRE     = regexp.MustCompile(`django\.middleware\.csrf\.CsrfViewMiddleware|\bCSRFProtect\s*\(|\bcsrf\.init_app\s*\(|\bapp\.use\(\s*(?:csurf|csrf|csrfProtection)\b|\bcsrf\.Protect\s*\(|\brequire\(\s*["']csurf["']\s*\)`)
)

// matchCSRFOff fires on CSRF protection switched off.
func matchCSRFOff(c *lineCtx) bool {
	if c.Lang == LangPython && pyCSRFOffRE.MatchString(c.Bare) {
		return true
	}
	return optCSRFOffRE.MatchString(c.Bare)
}

// matchCSRFRemoved fires on a removed line that enabled CSRF protection,
// unless the same protection is still on a post-run line of the file (it
// moved).
func matchCSRFRemoved(c *lineCtx) bool {
	m := csrfOnRE.FindString(c.Bare)
	return m != "" && !strings.Contains(c.File, m)
}

var (
	pyRequestRE = regexp.MustCompile(`\brequest\.(?:args|form|values|files|json|GET|POST|FILES|data|query_params|path_params|match_info|query|get_json)\b`)
	jsRequestRE = regexp.MustCompile(`\b(?:req|request)\.(?:params|query|body|files|file|headers|cookies)\b|\bctx\.(?:params|query|request\.body)\b`)
	goRequestRE = regexp.MustCompile(`\br\.URL\.Query\(\)|\br\.(?:FormValue|PostFormValue|PathValue)\(|\bmux\.Vars\(|\bc\.(?:Param|Query|PostForm|DefaultQuery|FormValue)\(|\bchi\.URLParam\(`)
	pyFileSink  = regexp.MustCompile(`(?:^|[^\w.])open\s*\(|\bsend_file\s*\(|\bFileResponse\s*\(|\bos\.(?:remove|unlink|rmdir|listdir|scandir)\s*\(|\bshutil\.(?:rmtree|copy\w*|move)\s*\(|\.(?:read_text|read_bytes|write_text|write_bytes|unlink)\s*\(`)
	jsFileSink  = regexp.MustCompile(`\bfs\.(?:promises\.)?(?:readFile|readFileSync|createReadStream|createWriteStream|writeFile|writeFileSync|appendFile|appendFileSync|unlink|unlinkSync|rm|rmSync|readdir|readdirSync|open|openSync)\s*\(|\.(?:sendFile|download)\s*\(`)
	goFileSink  = regexp.MustCompile(`\bos\.(?:Open|OpenFile|ReadFile|WriteFile|Remove|RemoveAll|Create|ReadDir)\s*\(|\bhttp\.ServeFile\s*\(|\bioutil\.ReadFile\s*\(|\bc\.(?:File|FileAttachment)\s*\(`)
	sanitizerRE = regexp.MustCompile(`\bsecure_filename\s*\(|\bsafe_join\s*\(|\bos\.path\.basename\s*\(|\.resolve\(\)|\brealpath\s*\(|\bis_relative_to\s*\(|\bcommonpath\s*\(|\bpath\.basename\s*\(|\.startsWith\s*\(|\bfilepath\.Base\s*\(|\bstrings\.HasPrefix\s*\(|\bfilepath\.Rel\s*\(|\bfilepath\.IsLocal\s*\(|\bos\.OpenRoot\b|\bsecurejoin\b|\broot\s*:`)
	pyRouteRE   = regexp.MustCompile(`@\w+\.(?:route|get|post|put|delete)\(\s*["'][^"']*<(?:\w+:)?(\w+)>`)
)

// matchPathTraversal fires on a file sink fed by request input.
func matchPathTraversal(c *lineCtx) bool {
	req, sink := pyRequestRE, pyFileSink
	switch c.Lang {
	case LangJavaScript, LangTypeScript:
		req, sink = jsRequestRE, jsFileSink
	case LangGo:
		req, sink = goRequestRE, goFileSink
	}
	loc := sink.FindStringIndex(c.Code)
	if loc == nil || sanitizerRE.MatchString(c.window(windowLines)) {
		return false
	}
	args := c.Text[loc[1]:]
	if req.MatchString(args) {
		return true
	}
	tainted := map[string]bool{}
	for _, l := range c.Before {
		for _, m := range assignRE.FindAllStringSubmatch(l, -1) {
			if req.MatchString(m[2]) {
				tainted[m[1]] = true
			}
		}
		for _, m := range destructureRE.FindAllStringSubmatch(l, -1) {
			if req.MatchString(m[2]) {
				for _, n := range identRE.FindAllString(m[1], -1) {
					tainted[n] = true
				}
			}
		}
		if m := pyRouteRE.FindStringSubmatch(l); m != nil {
			tainted[m[1]] = true
		}
	}
	for _, n := range identRE.FindAllString(codeOf(c.Lang, args), -1) {
		if tainted[n] {
			return true
		}
	}
	return false
}

var (
	secretNameRE = regexp.MustCompile(`(?i)token|secret|passw|nonce|\botp\b|\bsalt\b|session_?id|sessionid|csrf|api_?key|apikey|reset_?code|verification_?code|auth_?code|invite_?code|magic_?link|one_?time`)
	pyRandomRE   = regexp.MustCompile(`\brandom\.(?:random|randint|randrange|choice|choices|sample|getrandbits|uniform|randbytes)\s*\(`)
	jsRandomRE   = regexp.MustCompile(`\bMath\.random\s*\(`)
	goRandomRE   = regexp.MustCompile(`\brand\.(?:Int|Intn|IntN|Int31|Int31n|Int63|Int63n|Uint32|Uint64|Read|Float64|Perm|N)\s*\(`)
	goMathRandRE = regexp.MustCompile(`"math/rand(?:/v2)?"`)
)

// matchInsecureRandom fires on a non-cryptographic generator near a secret
// name.
func matchInsecureRandom(c *lineCtx) bool {
	switch c.Lang {
	case LangPython:
		if !pyRandomRE.MatchString(c.Code) {
			return false
		}
	case LangJavaScript, LangTypeScript:
		if !jsRandomRE.MatchString(c.Code) {
			return false
		}
	case LangGo:
		if !goRandomRE.MatchString(c.Code) || !goMathRandRE.MatchString(c.File) {
			return false
		}
	}
	before := c.Before
	if len(before) > 3 {
		before = before[len(before)-3:]
	}
	return secretNameRE.MatchString(codeOf(c.Lang, strings.Join(append(append([]string{}, before...), c.Text), "\n")))
}

var (
	assignRE      = regexp.MustCompile(`(?:^|[^\w.$])([A-Za-z_$][\w$]*)\s*(?::=|\+=|=)\s*([^=].*)$`)
	destructureRE = regexp.MustCompile(`\{\s*([\w$,\s:]+)\}\s*=\s*(.+)$`)
)

// assignedIn returns the right-hand side of the last assignment to name in
// lines (nearest last), joined with any += lines after it, or "".
func assignedIn(lines []string, name string) string {
	var parts []string
	for i := len(lines) - 1; i >= 0; i-- {
		for _, m := range assignRE.FindAllStringSubmatch(lines[i], -1) {
			if m[1] != name {
				continue
			}
			parts = append(parts, m[2])
			if !strings.Contains(lines[i], "+=") {
				return strings.Join(parts, "\n")
			}
		}
	}
	return strings.Join(parts, "\n")
}

// dynamicArg reports whether a call's first argument is built with dyn: on
// the line itself, or, for a bare variable, in its assignment above.
func dynamicArg(c *lineCtx, arg string, dyn *regexp.Regexp) bool {
	if dyn.MatchString(arg) {
		return true
	}
	if m := identRE.FindString(arg); m != "" && strings.TrimSpace(arg) == m {
		return dyn.MatchString(assignedIn(c.Before, m))
	}
	return false
}
