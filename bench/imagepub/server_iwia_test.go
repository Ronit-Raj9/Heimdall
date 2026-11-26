package imagepub

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// leftoverTars lists the upload temp files left in dir.
func leftoverTars(t *testing.T, dir string) []string {
	t.Helper()
	m, err := filepath.Glob(filepath.Join(dir, "publish-*.tar"))
	if err != nil {
		t.Fatal(err)
	}
	return m
}

// TestPublishBodyLimitAndTempCleanup: an upload over the size limit is
// refused with 413 before crane runs, and no upload file stays in the temp
// folder after a refused or a successful publish.
func TestPublishBodyLimitAndTempCleanup(t *testing.T) {
	s, marker := testServer(t)
	s.MaxBytes = 4
	rec := publish(t, s, "bench/a:1", "tok")
	if rec.Code != http.StatusRequestEntityTooLarge || !strings.Contains(rec.Body.String(), "image too large") {
		t.Fatalf("over the limit: code %d body %s", rec.Code, rec.Body.String())
	}
	if _, err := os.Stat(marker); err == nil {
		t.Fatal("crane ran for an upload over the limit")
	}
	if left := leftoverTars(t, s.TempDir); len(left) != 0 {
		t.Fatalf("temp files left after a refusal: %v", left)
	}
	s.MaxBytes = 0
	if rec := publish(t, s, "bench/a:1", "tok"); rec.Code != http.StatusOK {
		t.Fatalf("within the limit: code %d body %s", rec.Code, rec.Body.String())
	}
	if left := leftoverTars(t, s.TempDir); len(left) != 0 {
		t.Fatalf("temp files left after a publish: %v", left)
	}
}

// TestPublishTokenNeedsBearerScheme: the token is accepted only as a bearer
// token, never as another scheme, a prefix or a longer string.
func TestPublishTokenNeedsBearerScheme(t *testing.T) {
	s, marker := testServer(t)
	for _, h := range []string{"tok", "Basic tok", "Bearer to", "Bearer tokx", "bearer tok"} {
		req := httptest.NewRequest(http.MethodPost, "/v1/publish?ref=bench/a:1", strings.NewReader("TARDATA"))
		req.Header.Set("Authorization", h)
		rec := httptest.NewRecorder()
		s.Handler().ServeHTTP(rec, req)
		if rec.Code != http.StatusUnauthorized {
			t.Errorf("Authorization %q: code %d", h, rec.Code)
		}
	}
	if _, err := os.Stat(marker); err == nil {
		t.Fatal("crane ran without the token")
	}
}
