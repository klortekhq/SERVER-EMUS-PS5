package httpapi

import (
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/klortekhq/server-emus-ps5/internal/catalog"
	"github.com/klortekhq/server-emus-ps5/internal/config"
)

func testServer(t *testing.T, token string) (*Server, catalog.Entry) {
	t.Helper()
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "disc.chd"), []byte("0123456789"), 0o644); err != nil {
		t.Fatal(err)
	}
	cat, err := catalog.New(config.Config{Libraries: []config.Library{{
		Name: "PS1", System: "ps1", Path: root, Recursive: true, Extensions: []string{".chd"},
	}}})
	if err != nil {
		t.Fatal(err)
	}
	entries := cat.Entries("ps1")
	return New(cat, token), entries[0]
}

func TestRangeRead(t *testing.T) {
	server, entry := testServer(t, "")
	req := httptest.NewRequest(http.MethodGet, "/api/v1/files/"+entry.ID, nil)
	req.Header.Set("Range", "bytes=2-5")
	rec := httptest.NewRecorder()

	server.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusPartialContent {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	body, _ := io.ReadAll(rec.Result().Body)
	if string(body) != "2345" {
		t.Fatalf("body=%q", body)
	}
	if rec.Header().Get("Accept-Ranges") != "bytes" {
		t.Fatal("Accept-Ranges missing")
	}
}

func TestHeadAndCatalogDoNotExposeHostPath(t *testing.T) {
	server, entry := testServer(t, "")

	head := httptest.NewRequest(http.MethodHead, "/api/v1/files/"+entry.ID, nil)
	headRec := httptest.NewRecorder()
	server.Handler().ServeHTTP(headRec, head)
	if headRec.Code != http.StatusOK {
		t.Fatalf("HEAD status=%d", headRec.Code)
	}
	if headRec.Header().Get("Content-Length") != "10" {
		t.Fatalf("unexpected content length %q", headRec.Header().Get("Content-Length"))
	}

	games := httptest.NewRequest(http.MethodGet, "/api/v1/games?system=ps1", nil)
	gamesRec := httptest.NewRecorder()
	server.Handler().ServeHTTP(gamesRec, games)
	if strings.Contains(gamesRec.Body.String(), entry.HostPath) {
		t.Fatal("catalog leaked physical host path")
	}
}

func TestBearerToken(t *testing.T) {
	server, _ := testServer(t, "secret")

	req := httptest.NewRequest(http.MethodGet, "/api/v1/health", nil)
	rec := httptest.NewRecorder()
	server.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status=%d want 401", rec.Code)
	}

	req = httptest.NewRequest(http.MethodGet, "/api/v1/health", nil)
	req.Header.Set("Authorization", "Bearer secret")
	rec = httptest.NewRecorder()
	server.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d want 200", rec.Code)
	}
}
