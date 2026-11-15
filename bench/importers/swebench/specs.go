package swebench

import "strings"

// Test commands of the original SWE-bench harness (swebench 4.x,
// harness/constants/python.py, MIT), for rows that carry no command of their
// own: original SWE-bench and Impossible-SWEbench. SWE-rebench rows carry
// install_config.test_cmd and SWE-bench-Live rows test_cmds; those win.
const (
	cmdPytest        = "pytest -rA"
	cmdAstropyLegacy = "pytest -rA -vv -o console_output_style=classic --tb=no"
	cmdDjango        = "./tests/runtests.py --verbosity 2 --settings=test_sqlite --parallel 1"
	cmdDjangoSerial  = "./tests/runtests.py --verbosity 2"
	cmdSeaborn       = "pytest --no-header -rA"
	cmdSphinx        = "tox --current-env -epy39 -v --"
	cmdSympy         = "PYTHONWARNINGS='ignore::UserWarning,ignore::SyntaxWarning' bin/test -C --verbose"
)

// Log parsers grade.py implements, named as upstream names them.
const (
	parserPytest        = "parse_log_pytest"
	parserPytestOptions = "parse_log_pytest_options"
	parserPytestV2      = "parse_log_pytest_v2"
	parserDjango        = "parse_log_django"
	parserSympy         = "parse_log_sympy"
	parserSeaborn       = "parse_log_seaborn"
	parserMatplotlib    = "parse_log_matplotlib"
)

// parserAliases maps every parser name upstream uses to the implementation
// grade.py has (upstream defines most per-repo parsers as aliases).
var parserAliases = map[string]string{
	"pytest":                parserPytest,
	parserPytest:            parserPytest,
	"parse_log_astroid":     parserPytest,
	"parse_log_flask":       parserPytest,
	"parse_log_marshmallow": parserPytest,
	"parse_log_pvlib":       parserPytest,
	"parse_log_pyvista":     parserPytest,
	"parse_log_sqlfluff":    parserPytest,
	"parse_log_xarray":      parserPytest,
	parserPytestOptions:     parserPytestOptions,
	"parse_log_pydicom":     parserPytestOptions,
	"parse_log_requests":    parserPytestOptions,
	"parse_log_pylint":      parserPytestOptions,
	parserPytestV2:          parserPytestV2,
	"parse_log_astropy":     parserPytestV2,
	"parse_log_scikit":      parserPytestV2,
	"parse_log_sphinx":      parserPytestV2,
	parserDjango:            parserDjango,
	parserSympy:             parserSympy,
	parserSeaborn:           parserSeaborn,
	parserMatplotlib:        parserMatplotlib,
}

// repoParsers is upstream's parser per repository, for rows without a
// log_parser field.
var repoParsers = map[string]string{
	"astropy/astropy":              parserPytestV2,
	"django/django":                parserDjango,
	"marshmallow-code/marshmallow": parserPytest,
	"matplotlib/matplotlib":        parserMatplotlib,
	"mwaskom/seaborn":              parserSeaborn,
	"pallets/flask":                parserPytest,
	"psf/requests":                 parserPytestOptions,
	"pvlib/pvlib-python":           parserPytest,
	"pydata/xarray":                parserPytest,
	"pydicom/pydicom":              parserPytestOptions,
	"pylint-dev/astroid":           parserPytest,
	"pylint-dev/pylint":            parserPytestOptions,
	"pytest-dev/pytest":            parserPytest,
	"pyvista/pyvista":              parserPytest,
	"scikit-learn/scikit-learn":    parserPytestV2,
	"sqlfluff/sqlfluff":            parserPytest,
	"sphinx-doc/sphinx":            parserPytestV2,
	"sympy/sympy":                  parserSympy,
}

// legacyTestCmd returns upstream's test command for a repository and version
// of the original SWE-bench, and false for a repository it does not know.
// It is pure.
func legacyTestCmd(repo, version string) (string, bool) {
	switch repo {
	case "astropy/astropy":
		switch version {
		case "0.1", "0.2", "0.3", "0.4", "1.1", "1.2", "1.3":
			return cmdAstropyLegacy, true
		}
		return cmdPytest, true
	case "django/django":
		if version == "1.9" {
			return cmdDjangoSerial, true
		}
		return cmdDjango, true
	case "mwaskom/seaborn":
		return cmdSeaborn, true
	case "sphinx-doc/sphinx":
		return cmdSphinx, true
	case "sympy/sympy":
		return cmdSympy, true
	}
	if _, ok := repoParsers[repo]; ok {
		return cmdPytest, true
	}
	return "", false
}

// testDirectives are the test files a command runs, as upstream derives them
// from the test patch: every changed path except data files, and for Django
// the dotted module under tests/. It is pure.
func testDirectives(repo string, files []string) []string {
	var out []string
	for _, f := range files {
		if nonTestFile(f) {
			continue
		}
		if repo == "django/django" {
			f = strings.TrimSuffix(strings.TrimPrefix(f, "tests/"), ".py")
			f = strings.ReplaceAll(f, "/", ".")
		}
		out = append(out, f)
	}
	return out
}

// nonTestFile reports whether a changed path is data, not a test module.
func nonTestFile(p string) bool {
	for _, ext := range []string{".json", ".png", ".csv", ".txt", ".md", ".jpg", ".jpeg", ".pkl", ".yml", ".yaml", ".toml"} {
		if strings.HasSuffix(p, ext) {
			return true
		}
	}
	return false
}
