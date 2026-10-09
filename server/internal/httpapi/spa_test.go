package httpapi

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSPAHandler(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "index.html"), "<html>app</html>")
	writeFile(t, filepath.Join(dir, "assets", "index-abc123.js"), "console.log(1)")
	writeFile(t, filepath.Join(dir, "favicon.svg"), "<svg/>")

	h := spaHandler(dir)
	tests := []struct {
		path, wantBody, wantCache string
	}{
		{"/", "<html>app</html>", "no-cache"},
		{"/restore", "<html>app</html>", "no-cache"},
		{"/assets/index-abc123.js", "console.log(1)", "public, max-age=31536000, immutable"},
		{"/favicon.svg", "<svg/>", ""},
		{"/assets", "<html>app</html>", "no-cache"},
	}
	for _, tt := range tests {
		t.Run(tt.path, func(t *testing.T) {
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, tt.path, nil))
			if rec.Code != http.StatusOK {
				t.Fatalf("status = %d, want 200", rec.Code)
			}
			if body := rec.Body.String(); !strings.Contains(body, tt.wantBody) {
				t.Errorf("body = %q, want %q", body, tt.wantBody)
			}
			if got := rec.Header().Get("Cache-Control"); got != tt.wantCache {
				t.Errorf("Cache-Control = %q, want %q", got, tt.wantCache)
			}
		})
	}
}

func TestSPAHandlerBlocksTraversal(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "app", "index.html"), "<html>app</html>")
	writeFile(t, filepath.Join(dir, "secret.txt"), "nope")

	rec := httptest.NewRecorder()
	spaHandler(filepath.Join(dir, "app")).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/../secret.txt", nil))
	if strings.Contains(rec.Body.String(), "nope") {
		t.Fatalf("served a file outside WEB_DIR (status %d)", rec.Code)
	}
}

func TestSPAHandlerWithoutBuild(t *testing.T) {
	rec := httptest.NewRecorder()
	spaHandler("").ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))
	if rec.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404", rec.Code)
	}
}

func writeFile(t *testing.T, name, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(name), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(name, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}
