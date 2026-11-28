package emit

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/maximhq/bifrost/bench/issuebench"
)

// A builder step with its own heredoc (python - <<'PY') was joined with
// backslashes, and the admit build failed "unterminated heredoc" (real hosted
// run). Steps also run with bash -lc and the builder's time pins.
func TestSelfContainedDockerfileHeredocAndPins(t *testing.T) {
	env := issuebench.EnvSpec{BaseImage: "python:3.13-slim", Dockerfile: "FROM python:3.13-slim\nENV UV_EXCLUDE_NEWER=\"2026-04-02T12:53:27Z\"\n",
		Commands: []string{"python - <<'PY'\nprint(1)\nPY", "pip install -e ."}}
	df := SelfContainedDockerfile(env)
	for _, w := range []string{"SHELL [\"/bin/bash\", \"-lc\"]", "ENV UV_EXCLUDE_NEWER=\"2026-04-02T12:53:27Z\"", "RUN <<'HEIMDALL_STEP'\nset -e\npython - <<'PY'\nprint(1)\nPY\nHEIMDALL_STEP\n", "RUN pip install -e .\n"} {
		if !strings.Contains(df, w) {
			t.Fatalf("Dockerfile lacks %q:\n%s", w, df)
		}
	}
}

func TestDetectLicense(t *testing.T) {
	d := t.TempDir()
	if got := DetectLicense(d); got != LicenseUnknown {
		t.Fatalf("no file: %q", got)
	}
	_ = os.WriteFile(filepath.Join(d, "LICENSE"), []byte("Copyright 2026 Steven Loria\n\nPermission is hereby granted, free of charge, to any person obtaining a copy\nof this software"), 0o644)
	if got := DetectLicense(d); got != "MIT" {
		t.Fatalf("MIT: %q", got)
	}
	_ = os.WriteFile(filepath.Join(d, "LICENSE"), []byte("Some custom terms"), 0o644)
	if got := DetectLicense(d); got != LicenseUnknown {
		t.Fatalf("custom: %q", got)
	}
}
