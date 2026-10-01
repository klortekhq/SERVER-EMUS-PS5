package httpapi

import (
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
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

func TestTransportMetricsCountRangeTraffic(t *testing.T) {
	server, entry := testServer(t, "")

	rangeReq := httptest.NewRequest(http.MethodGet, "/api/v1/files/"+entry.ID, nil)
	rangeReq.Header.Set("Range", "bytes=2-5")
	rangeRec := httptest.NewRecorder()
	server.Handler().ServeHTTP(rangeRec, rangeReq)
	if rangeRec.Code != http.StatusPartialContent {
		t.Fatalf("range status=%d body=%s", rangeRec.Code, rangeRec.Body.String())
	}

	headReq := httptest.NewRequest(http.MethodHead, "/api/v1/files/"+entry.ID, nil)
	headRec := httptest.NewRecorder()
	server.Handler().ServeHTTP(headRec, headReq)
	if headRec.Code != http.StatusOK {
		t.Fatalf("HEAD status=%d", headRec.Code)
	}

	metricsReq := httptest.NewRequest(http.MethodGet, "/api/v1/metrics", nil)
	metricsRec := httptest.NewRecorder()
	server.Handler().ServeHTTP(metricsRec, metricsReq)
	if metricsRec.Code != http.StatusOK {
		t.Fatalf("metrics status=%d body=%s", metricsRec.Code, metricsRec.Body.String())
	}

	for _, want := range []string{
		`"file_get_requests":1`,
		`"file_head_requests":1`,
		`"range_requests":1`,
		`"multi_range_requests":0`,
		`"full_get_requests":0`,
		`"bytes_served":4`,
		`"range_bytes_served":4`,
		`"full_get_bytes_served":0`,
		`"file_request_duration_us_total":`,
		`"file_request_duration_us_max":`,
		`"not_found":0`,
		`"errors":0`,
	} {
		if !strings.Contains(metricsRec.Body.String(), want) {
			t.Fatalf("metrics response missing %s: %s", want, metricsRec.Body.String())
		}
	}

	healthReq := httptest.NewRequest(http.MethodGet, "/api/v1/health", nil)
	healthRec := httptest.NewRecorder()
	server.Handler().ServeHTTP(healthRec, healthReq)
	if !strings.Contains(healthRec.Body.String(), `"transport_metrics":true`) {
		t.Fatalf("health did not advertise metrics: %s", healthRec.Body.String())
	}
}

func TestTransportMetricsClassifiesStaleIfRangeFallbackAsFullGet(t *testing.T) {
	server, entry := testServer(t, "")

	req := httptest.NewRequest(http.MethodGet, "/api/v1/files/"+entry.ID, nil)
	req.Header.Set("Range", "bytes=2-5")
	req.Header.Set("If-Range", "\"stale-etag\"")
	rec := httptest.NewRecorder()
	server.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("stale If-Range status=%d body=%s", rec.Code, rec.Body.String())
	}
	if rec.Body.String() != "0123456789" {
		t.Fatalf("stale If-Range body=%q", rec.Body.String())
	}

	metricsReq := httptest.NewRequest(http.MethodGet, "/api/v1/metrics", nil)
	metricsRec := httptest.NewRecorder()
	server.Handler().ServeHTTP(metricsRec, metricsReq)
	for _, want := range []string{
		`"file_get_requests":1`,
		`"range_requests":1`,
		`"multi_range_requests":0`,
		`"full_get_requests":1`,
		`"bytes_served":10`,
		`"range_bytes_served":0`,
		`"full_get_bytes_served":10`,
	} {
		if !strings.Contains(metricsRec.Body.String(), want) {
			t.Fatalf("If-Range metrics missing %s: %s", want, metricsRec.Body.String())
		}
	}
}

func TestTransportMetricsResetRequiresTokenAndClearsCounters(t *testing.T) {
	unsecured, unsecuredEntry := testServer(t, "")
	rangeReq := httptest.NewRequest(http.MethodGet, "/api/v1/files/"+unsecuredEntry.ID, nil)
	rangeReq.Header.Set("Range", "bytes=0-1")
	rangeRec := httptest.NewRecorder()
	unsecured.Handler().ServeHTTP(rangeRec, rangeReq)

	resetReq := httptest.NewRequest(http.MethodPost, "/api/v1/admin/metrics/reset", nil)
	resetRec := httptest.NewRecorder()
	unsecured.Handler().ServeHTTP(resetRec, resetReq)
	if resetRec.Code != http.StatusForbidden {
		t.Fatalf("unsecured reset status=%d want 403", resetRec.Code)
	}

	secured, entry := testServer(t, "secret")
	rangeReq = httptest.NewRequest(http.MethodGet, "/api/v1/files/"+entry.ID, nil)
	rangeReq.Header.Set("Authorization", "Bearer secret")
	rangeReq.Header.Set("Range", "bytes=0-1")
	rangeRec = httptest.NewRecorder()
	secured.Handler().ServeHTTP(rangeRec, rangeReq)
	if rangeRec.Code != http.StatusPartialContent {
		t.Fatalf("secured range status=%d", rangeRec.Code)
	}

	resetReq = httptest.NewRequest(http.MethodPost, "/api/v1/admin/metrics/reset", nil)
	resetReq.Header.Set("Authorization", "Bearer secret")
	resetRec = httptest.NewRecorder()
	secured.Handler().ServeHTTP(resetRec, resetReq)
	if resetRec.Code != http.StatusOK {
		t.Fatalf("secured reset status=%d body=%s", resetRec.Code, resetRec.Body.String())
	}

	metricsReq := httptest.NewRequest(http.MethodGet, "/api/v1/metrics", nil)
	metricsReq.Header.Set("Authorization", "Bearer secret")
	metricsRec := httptest.NewRecorder()
	secured.Handler().ServeHTTP(metricsRec, metricsReq)
	for _, want := range []string{
		`"file_get_requests":0`,
		`"range_requests":0`,
		`"multi_range_requests":0`,
		`"bytes_served":0`,
		`"range_bytes_served":0`,
		`"full_get_bytes_served":0`,
		`"file_request_duration_us_total":0`,
		`"file_request_duration_us_max":0`,
		`"errors":0`,
	} {
		if !strings.Contains(metricsRec.Body.String(), want) {
			t.Fatalf("reset metrics missing %s: %s", want, metricsRec.Body.String())
		}
	}

	healthReq := httptest.NewRequest(http.MethodGet, "/api/v1/health", nil)
	healthReq.Header.Set("Authorization", "Bearer secret")
	healthRec := httptest.NewRecorder()
	secured.Handler().ServeHTTP(healthRec, healthReq)
	if !strings.Contains(healthRec.Body.String(), `"transport_metrics_reset":true`) {
		t.Fatalf("secured health did not advertise metrics reset: %s", healthRec.Body.String())
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

func TestLibrariesEndpointDoesNotExposeHostPaths(t *testing.T) {
	server, entry := testServer(t, "")

	req := httptest.NewRequest(http.MethodGet, "/api/v1/libraries", nil)
	rec := httptest.NewRecorder()
	server.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	if strings.Contains(body, entry.HostPath) || strings.Contains(body, filepath.Dir(entry.HostPath)) {
		t.Fatal("libraries endpoint leaked physical host path")
	}
	for _, want := range []string{`"name":"PS1"`, `"system":"ps1"`, `"files":1`} {
		if !strings.Contains(body, want) {
			t.Fatalf("libraries response missing %s: %s", want, body)
		}
	}
}

func TestCatalogConditionalCaching(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "disc1.chd"), []byte("ONE"), 0o644); err != nil {
		t.Fatal(err)
	}
	cat, err := catalog.New(config.Config{Libraries: []config.Library{{
		Name: "PS1", System: "ps1", Path: root, Recursive: true, Extensions: []string{".chd"},
	}}})
	if err != nil {
		t.Fatal(err)
	}
	server := New(cat, "")

	first := httptest.NewRequest(http.MethodGet, "/api/v1/games?system=ps1", nil)
	firstRec := httptest.NewRecorder()
	server.Handler().ServeHTTP(firstRec, first)
	if firstRec.Code != http.StatusOK {
		t.Fatalf("initial status=%d body=%s", firstRec.Code, firstRec.Body.String())
	}
	etag := firstRec.Header().Get("ETag")
	if etag == "" {
		t.Fatal("catalog ETag missing")
	}

	unchangedRevision := cat.Revision()
	if err := cat.Rebuild(); err != nil {
		t.Fatal(err)
	}
	if cat.Revision() != unchangedRevision {
		t.Fatalf("unchanged rebuild changed revision: %q -> %q", unchangedRevision, cat.Revision())
	}

	cached := httptest.NewRequest(http.MethodGet, "/api/v1/games?system=ps1", nil)
	cached.Header.Set("If-None-Match", etag)
	cachedRec := httptest.NewRecorder()
	server.Handler().ServeHTTP(cachedRec, cached)
	if cachedRec.Code != http.StatusNotModified {
		t.Fatalf("cached status=%d want 304", cachedRec.Code)
	}
	if cachedRec.Body.Len() != 0 {
		t.Fatalf("304 returned body %q", cachedRec.Body.String())
	}

	if err := os.WriteFile(filepath.Join(root, "disc2.chd"), []byte("TWO"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := cat.Rebuild(); err != nil {
		t.Fatal(err)
	}
	if cat.Revision() == etag {
		t.Fatal("catalog revision did not change after adding a game")
	}

	stale := httptest.NewRequest(http.MethodGet, "/api/v1/games?system=ps1", nil)
	stale.Header.Set("If-None-Match", etag)
	staleRec := httptest.NewRecorder()
	server.Handler().ServeHTTP(staleRec, stale)
	if staleRec.Code != http.StatusOK {
		t.Fatalf("stale status=%d body=%s", staleRec.Code, staleRec.Body.String())
	}
	if staleRec.Header().Get("ETag") == etag {
		t.Fatal("stale request received old catalog ETag")
	}
	if !strings.Contains(staleRec.Body.String(), "disc2.chd") {
		t.Fatalf("updated catalog missing new game: %s", staleRec.Body.String())
	}

	weak := httptest.NewRequest(http.MethodGet, "/api/v1/libraries", nil)
	weak.Header.Set("If-None-Match", "W/"+cat.Revision())
	weakRec := httptest.NewRecorder()
	server.Handler().ServeHTTP(weakRec, weak)
	if weakRec.Code != http.StatusNotModified {
		t.Fatalf("weak ETag status=%d want 304", weakRec.Code)
	}
}

func TestAuthenticatedCatalogRebuild(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "disc1.chd"), []byte("ONE"), 0o644); err != nil {
		t.Fatal(err)
	}
	cat, err := catalog.New(config.Config{Libraries: []config.Library{{
		Name: "PS1", System: "ps1", Path: root, Recursive: true, Extensions: []string{".chd"},
	}}})
	if err != nil {
		t.Fatal(err)
	}
	server := New(cat, "secret")

	if got := len(cat.Entries("ps1")); got != 1 {
		t.Fatalf("initial entries=%d want 1", got)
	}
	if err := os.WriteFile(filepath.Join(root, "disc2.chd"), []byte("TWO"), 0o644); err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest(http.MethodPost, "/api/v1/catalog/rebuild", nil)
	req.Header.Set("Authorization", "Bearer secret")
	rec := httptest.NewRecorder()
	server.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("rebuild status=%d body=%s", rec.Code, rec.Body.String())
	}
	if got := len(cat.Entries("ps1")); got != 2 {
		t.Fatalf("rebuilt entries=%d want 2", got)
	}
	for _, want := range []string{`"ok":true`, `"files":2`} {
		if !strings.Contains(rec.Body.String(), want) {
			t.Fatalf("rebuild response missing %s: %s", want, rec.Body.String())
		}
	}
}

func TestCatalogRebuildRequiresConfiguredToken(t *testing.T) {
	server, _ := testServer(t, "")
	req := httptest.NewRequest(http.MethodPost, "/api/v1/catalog/rebuild", nil)
	rec := httptest.NewRecorder()
	server.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("status=%d want 403", rec.Code)
	}
}

func TestHealthCapabilities(t *testing.T) {
	server, _ := testServer(t, "")

	req := httptest.NewRequest(http.MethodGet, "/api/v1/health", nil)
	rec := httptest.NewRecorder()
	server.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	for _, want := range []string{
		`"service":"SERVER-EMUS-PS5"`,
		`"api":"v1"`,
		`"byte_ranges":true`,
		`"multi_ranges":true`,
		`"max_range_parts":16`,
		`"anchored_virtual_sidecars":true`,
		`"catalog_discovery":true`,
		`"catalog_etag":true`,
		`"catalog_rebuild":false`,
	} {
		if !strings.Contains(rec.Body.String(), want) {
			t.Fatalf("health response missing %s: %s", want, rec.Body.String())
		}
	}

	secured, _ := testServer(t, "secret")
	req = httptest.NewRequest(http.MethodGet, "/api/v1/health", nil)
	req.Header.Set("Authorization", "Bearer secret")
	rec = httptest.NewRecorder()
	secured.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("secured health status=%d body=%s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"catalog_rebuild":true`) {
		t.Fatalf("secured health did not advertise rebuild capability: %s", rec.Body.String())
	}
}

func TestAdminUIIsPublicShellWhileAPIStaysProtected(t *testing.T) {
	server, entry := testServer(t, "secret")

	rootReq := httptest.NewRequest(http.MethodGet, "/", nil)
	rootRec := httptest.NewRecorder()
	server.Handler().ServeHTTP(rootRec, rootReq)
	if rootRec.Code != http.StatusTemporaryRedirect {
		t.Fatalf("root status=%d want %d", rootRec.Code, http.StatusTemporaryRedirect)
	}
	if got := rootRec.Header().Get("Location"); got != "/admin/" {
		t.Fatalf("root redirect=%q", got)
	}

	uiReq := httptest.NewRequest(http.MethodGet, "/admin/", nil)
	uiRec := httptest.NewRecorder()
	server.Handler().ServeHTTP(uiRec, uiReq)
	if uiRec.Code != http.StatusOK {
		t.Fatalf("admin status=%d body=%s", uiRec.Code, uiRec.Body.String())
	}
	body := uiRec.Body.String()
	if !strings.Contains(body, "SERVER-EMUS-PS5") ||
		!strings.Contains(body, "sessionStorage") ||
		!strings.Contains(body, "/api/v1/admin/libraries") {
		t.Fatalf("admin shell missing expected controls: %s", body)
	}
	if strings.Contains(body, "secret") ||
		strings.Contains(body, entry.HostPath) ||
		strings.Contains(body, filepath.Dir(entry.HostPath)) {
		t.Fatal("admin shell leaked server secret or physical host path")
	}
	if got := uiRec.Header().Get("Cache-Control"); got != "no-store" {
		t.Fatalf("admin Cache-Control=%q", got)
	}
	if got := uiRec.Header().Get("Content-Security-Policy"); got == "" {
		t.Fatal("admin shell missing CSP")
	}

	apiReq := httptest.NewRequest(http.MethodGet, "/api/v1/health", nil)
	apiRec := httptest.NewRecorder()
	server.Handler().ServeHTTP(apiRec, apiReq)
	if apiRec.Code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated API status=%d want 401", apiRec.Code)
	}

	authReq := httptest.NewRequest(http.MethodGet, "/api/v1/health", nil)
	authReq.Header.Set("Authorization", "Bearer secret")
	authRec := httptest.NewRecorder()
	server.Handler().ServeHTTP(authRec, authReq)
	if authRec.Code != http.StatusOK {
		t.Fatalf("authenticated health status=%d body=%s", authRec.Code, authRec.Body.String())
	}
	if !strings.Contains(authRec.Body.String(), `"web_admin":true`) {
		t.Fatalf("health did not advertise web admin: %s", authRec.Body.String())
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


func TestManagedLibraryReplacementPersistsWithoutLeakingPaths(t *testing.T) {
	root1 := t.TempDir()
	root2 := t.TempDir()
	if err := os.WriteFile(filepath.Join(root1, "old.chd"), []byte("OLD"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root2, "new.chd"), []byte("NEW"), 0o644); err != nil {
		t.Fatal(err)
	}

	cfg, err := config.Normalize(config.Config{
		Listen: "127.0.0.1:8787",
		Token:  "secret",
		Libraries: []config.Library{{
			Name: "OLD", System: "ps1", Path: root1, Recursive: true, Extensions: []string{".chd"},
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	configPath := filepath.Join(t.TempDir(), "config.json")
	if err := config.Save(configPath, cfg); err != nil {
		t.Fatal(err)
	}
	cat, err := catalog.New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	server := NewManaged(cat, cfg, configPath)

	body := `{"libraries":[{"name":"NEW","system":"dreamcast","path":` + strconv.Quote(root2) + `,"recursive":true,"extensions":["chd","CHD"]}]}`
	req := httptest.NewRequest(http.MethodPut, "/api/v1/admin/libraries", strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer secret")
	rec := httptest.NewRecorder()
	server.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	if strings.Contains(rec.Body.String(), root1) || strings.Contains(rec.Body.String(), root2) {
		t.Fatal("admin response leaked physical library path")
	}
	if got := cat.Entries("ps1"); len(got) != 0 {
		t.Fatalf("old catalog still active: %d entries", len(got))
	}
	if got := cat.Entries("dreamcast"); len(got) != 1 || got[0].Name != "new.chd" {
		t.Fatalf("replacement catalog mismatch: %+v", got)
	}

	saved, err := config.Load(configPath)
	if err != nil {
		t.Fatal(err)
	}
	if len(saved.Libraries) != 1 || saved.Libraries[0].Name != "NEW" ||
		saved.Libraries[0].Path != filepath.Clean(root2) {
		t.Fatalf("persisted config mismatch: %+v", saved.Libraries)
	}
	if len(saved.Libraries[0].Extensions) != 1 || saved.Libraries[0].Extensions[0] != ".chd" {
		t.Fatalf("extensions were not normalized/deduplicated: %+v", saved.Libraries[0].Extensions)
	}

	health := httptest.NewRequest(http.MethodGet, "/api/v1/health", nil)
	health.Header.Set("Authorization", "Bearer secret")
	healthRec := httptest.NewRecorder()
	server.Handler().ServeHTTP(healthRec, health)
	if !strings.Contains(healthRec.Body.String(), `"library_editing":true`) {
		t.Fatalf("managed server did not advertise library editing: %s", healthRec.Body.String())
	}
}

func TestManagedLibraryReplacementRejectsInvalidPathWithoutMutation(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "disc.chd"), []byte("DATA"), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err := config.Normalize(config.Config{
		Token: "secret",
		Libraries: []config.Library{{
			Name: "PS1", System: "ps1", Path: root, Recursive: true, Extensions: []string{".chd"},
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	configPath := filepath.Join(t.TempDir(), "config.json")
	if err := config.Save(configPath, cfg); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatal(err)
	}
	cat, err := catalog.New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	revision := cat.Revision()
	server := NewManaged(cat, cfg, configPath)

	missing := filepath.Join(t.TempDir(), "missing")
	body := `{"libraries":[{"name":"BAD","system":"ps1","path":` + strconv.Quote(missing) + `,"recursive":true}]}`
	req := httptest.NewRequest(http.MethodPut, "/api/v1/admin/libraries", strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer secret")
	rec := httptest.NewRecorder()
	server.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status=%d want 400 body=%s", rec.Code, rec.Body.String())
	}
	if strings.Contains(rec.Body.String(), missing) {
		t.Fatal("invalid library response leaked physical path")
	}
	if cat.Revision() != revision || len(cat.Entries("ps1")) != 1 {
		t.Fatal("invalid library request mutated live catalog")
	}
	after, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(after) != string(before) {
		t.Fatal("invalid library request mutated persisted config")
	}
}

func TestMultiRangeReadUsesMultipartResponse(t *testing.T) {
	server, entry := testServer(t, "")
	req := httptest.NewRequest(http.MethodGet, "/api/v1/files/"+entry.ID, nil)
	req.Header.Set("Range", "bytes=0-1,8-9")
	rec := httptest.NewRecorder()

	server.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusPartialContent {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}

	mediaType, params, err := mime.ParseMediaType(rec.Header().Get("Content-Type"))
	if err != nil {
		t.Fatal(err)
	}
	if mediaType != "multipart/byteranges" {
		t.Fatalf("content type=%q", mediaType)
	}

	reader := multipart.NewReader(rec.Result().Body, params["boundary"])
	var payloads []string
	var ranges []string
	for {
		part, err := reader.NextPart()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		body, err := io.ReadAll(part)
		if err != nil {
			t.Fatal(err)
		}
		payloads = append(payloads, string(body))
		ranges = append(ranges, part.Header.Get("Content-Range"))
	}

	if len(payloads) != 2 ||
		payloads[0] != "01" ||
		payloads[1] != "89" {
		t.Fatalf("multipart payloads=%q", payloads)
	}
	if len(ranges) != 2 ||
		ranges[0] != "bytes 0-1/10" ||
		ranges[1] != "bytes 8-9/10" {
		t.Fatalf("multipart ranges=%q", ranges)
	}

	metricsReq := httptest.NewRequest(http.MethodGet, "/api/v1/metrics", nil)
	metricsRec := httptest.NewRecorder()
	server.Handler().ServeHTTP(metricsRec, metricsReq)
	if !strings.Contains(metricsRec.Body.String(), `"multi_range_requests":1`) {
		t.Fatalf("multi-range metric missing: %s", metricsRec.Body.String())
	}
}

func TestMultiRangeReadRejectsExcessiveParts(t *testing.T) {
	server, entry := testServer(t, "")
	parts := make([]string, maxRangeParts+1)
	for i := range parts {
		parts[i] = strconv.Itoa(i) + "-" + strconv.Itoa(i)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/v1/files/"+entry.ID, nil)
	req.Header.Set("Range", "bytes="+strings.Join(parts, ","))
	rec := httptest.NewRecorder()

	server.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusRequestedRangeNotSatisfiable {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"max_range_parts":16`) {
		t.Fatalf("limit response missing: %s", rec.Body.String())
	}
}
