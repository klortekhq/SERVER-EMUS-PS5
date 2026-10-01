package httpapi

import (
	"crypto/subtle"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"strings"

	"github.com/klortekhq/server-emus-ps5/internal/catalog"
)

type Server struct {
	catalog *catalog.Catalog
	token   string
}

func New(cat *catalog.Catalog, token string) *Server {
	return &Server{catalog: cat, token: strings.TrimSpace(token)}
}

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v1/health", s.health)
	mux.HandleFunc("/api/v1/systems", s.systems)
	mux.HandleFunc("/api/v1/libraries", s.libraries)
	mux.HandleFunc("/api/v1/catalog/rebuild", s.rebuildCatalog)
	mux.HandleFunc("/api/v1/games", s.games)
	mux.HandleFunc("/api/v1/files/", s.file)
	return s.auth(mux)
}

func (s *Server) auth(next http.Handler) http.Handler {
	if s.token == "" {
		return next
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		const prefix = "Bearer "
		header := r.Header.Get("Authorization")
		if !strings.HasPrefix(header, prefix) {
			writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
			return
		}
		got := strings.TrimSpace(strings.TrimPrefix(header, prefix))
		if subtle.ConstantTimeCompare([]byte(got), []byte(s.token)) != 1 {
			writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (s *Server) health(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		methodNotAllowed(w, http.MethodGet)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"ok":      true,
		"service": "SERVER-EMUS-PS5",
		"api":     "v1",
	})
}

func (s *Server) systems(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		methodNotAllowed(w, http.MethodGet)
		return
	}
	if catalogNotModified(w, r, s.catalog.Revision()) {
		return
	}
	writeJSON(w, http.StatusOK, s.catalog.Systems())
}
func (s *Server) libraries(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		methodNotAllowed(w, http.MethodGet)
		return
	}
	if catalogNotModified(w, r, s.catalog.Revision()) {
		return
	}
	writeJSON(w, http.StatusOK, s.catalog.Libraries())
}
func (s *Server) rebuildCatalog(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		methodNotAllowed(w, http.MethodPost)
		return
	}
	// Read-only streaming may deliberately run without authentication on a
	// trusted LAN. Administrative mutations never do: require a configured
	// bearer token before exposing a remote rescan trigger.
	if s.token == "" {
		writeJSON(w, http.StatusForbidden, map[string]string{
			"error": "catalog rebuild is disabled without a bearer token",
		})
		return
	}
	if err := s.catalog.Rebuild(); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{
			"error": "catalog rebuild failed",
		})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"ok":           true,
		"catalog_etag": s.catalog.Revision(),
		"systems":      s.catalog.Systems(),
		"libraries":    s.catalog.Libraries(),
	})
}

func (s *Server) games(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		methodNotAllowed(w, http.MethodGet)
		return
	}
	if catalogNotModified(w, r, s.catalog.Revision()) {
		return
	}
	writeJSON(w, http.StatusOK, s.catalog.Entries(r.URL.Query().Get("system")))
}
func (s *Server) file(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.Header().Set("Allow", "GET, HEAD")
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}

	id := strings.TrimPrefix(r.URL.Path, "/api/v1/files/")
	if id == "" || strings.Contains(id, "/") || strings.Contains(id, "\\") {
		http.NotFound(w, r)
		return
	}

	virtualPath := r.URL.Query().Get("path")
	f, entry, err := s.catalog.OpenVirtual(id, virtualPath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			http.NotFound(w, r)
			return
		}
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "open file failed"})
		return
	}
	defer f.Close()

	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Accept-Ranges", "bytes")
	w.Header().Set("ETag", entry.ETag)
	w.Header().Set("X-Emu-System", entry.System)
	w.Header().Set("X-Emu-Library", entry.Library)
	if virtualPath != "" {
		w.Header().Set("X-Emu-Relative-Path", entry.RelativePath)
	}

	http.ServeContent(w, r, entry.Name, entry.ModifiedAt, f)
}

func catalogNotModified(w http.ResponseWriter, r *http.Request, etag string) bool {
	if etag == "" {
		return false
	}
	w.Header().Set("ETag", etag)

	header := r.Header.Get("If-None-Match")
	if header == "" {
		return false
	}
	for _, candidate := range strings.Split(header, ",") {
		candidate = strings.TrimSpace(candidate)
		if candidate == "*" {
			w.WriteHeader(http.StatusNotModified)
			return true
		}
		if strings.HasPrefix(candidate, "W/") {
			candidate = strings.TrimSpace(strings.TrimPrefix(candidate, "W/"))
		}
		if candidate == etag {
			w.WriteHeader(http.StatusNotModified)
			return true
		}
	}
	return false
}

func methodNotAllowed(w http.ResponseWriter, allow string) {
	w.Header().Set("Allow", allow)
	writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}
