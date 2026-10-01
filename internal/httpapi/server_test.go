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


func TestAnchoredSidecarRead(t *testing.T) {
	root := t.TempDir()
	gameDir := filepath.Join(root, "game")
	if err := os.MkdirAll(gameDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(gameDir, "disc.cue"), []byte("FILE \"track.bin\" BINARY\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(gameDir, "track.bin"), []byte("TRACK-DATA"), 0o644); err != nil {
		t.Fatal(err)
	}

	cat, err := catalog.New(config.Config{Libraries: []config.Library{{
		Name: "PS1", System: "ps1", Path: root, Recursive: true, Extensions: []string{".cue"},
	}}})
	if err != nil {
		t.Fatal(err)
	}
	var cue catalog.Entry
	for _, entry := range cat.Entries("ps1") {
		if entry.Name == "disc.cue" {
			cue = entry
			break
		}
	}
	if cue.ID == "" {
		t.Fatal("cue anchor not found")
	}

	server := New(cat, "")
	req := httptest.NewRequest(
		http.MethodGet,
		"/api/v1/files/"+cue.ID+"?path=track.bin",
		nil,
	)
	rec := httptest.NewRecorder()
	server.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	if rec.Body.String() != "TRACK-DATA" {
		t.Fatalf("sidecar body=%q", rec.Body.String())
	}
	if rec.Header().Get("X-Emu-Relative-Path") != "game/track.bin" {
		t.Fatalf("unexpected resolved path %q", rec.Header().Get("X-Emu-Relative-Path"))
	}

	escape := httptest.NewRequest(
		http.MethodGet,
		"/api/v1/files/"+cue.ID+"?path=../../outside.bin",
		nil,
	)
	escapeRec := httptest.NewRecorder()
	server.Handler().ServeHTTP(escapeRec, escape)
	if escapeRec.Code != http.StatusNotFound {
		t.Fatalf("escape status=%d want 404", escapeRec.Code)
	}
}
