package httpapi

import (
	"crypto/subtle"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/klortekhq/server-emus-ps5/internal/catalog"
	"github.com/klortekhq/server-emus-ps5/internal/config"
)

type transportMetrics struct {
	fileGetRequests        atomic.Uint64
	fileHeadRequests       atomic.Uint64
	rangeRequests          atomic.Uint64
	fullGetRequests        atomic.Uint64
	sidecarRequests        atomic.Uint64
	bytesServed            atomic.Uint64
	rangeBytesServed       atomic.Uint64
	fullGetBytesServed     atomic.Uint64
	fileDurationMicros     atomic.Uint64
	fileDurationMicrosMax  atomic.Uint64
	notFound               atomic.Uint64
	errors                 atomic.Uint64
}

func (m *transportMetrics) reset() {
	m.fileGetRequests.Store(0)
	m.fileHeadRequests.Store(0)
	m.rangeRequests.Store(0)
	m.fullGetRequests.Store(0)
	m.sidecarRequests.Store(0)
	m.bytesServed.Store(0)
	m.rangeBytesServed.Store(0)
	m.fullGetBytesServed.Store(0)
	m.fileDurationMicros.Store(0)
	m.fileDurationMicrosMax.Store(0)
	m.notFound.Store(0)
	m.errors.Store(0)
}

func atomicMax(target *atomic.Uint64, value uint64) {
	for {
		current := target.Load()
		if value <= current || target.CompareAndSwap(current, value) {
			return
		}
	}
}

type Server struct {
	catalog    *catalog.Catalog
	token      string
	configPath string
	cfg        config.Config
	adminMu    sync.Mutex
	started    time.Time
	metrics    transportMetrics
}

func New(cat *catalog.Catalog, token string) *Server {
	return &Server{
		catalog: cat,
		token:   strings.TrimSpace(token),
		started: time.Now().UTC(),
	}
}

func NewManaged(cat *catalog.Catalog, cfg config.Config, configPath string) *Server {
	return &Server{
		catalog:    cat,
		token:      strings.TrimSpace(cfg.Token),
		configPath: strings.TrimSpace(configPath),
		cfg:        cfg,
		started:    time.Now().UTC(),
	}
}

func (s *Server) Handler() http.Handler {
	api := http.NewServeMux()
	api.HandleFunc("/api/v1/health", s.health)
	api.HandleFunc("/api/v1/metrics", s.transportMetrics)
	api.HandleFunc("/api/v1/admin/metrics/reset", s.resetTransportMetrics)
	api.HandleFunc("/api/v1/systems", s.systems)
	api.HandleFunc("/api/v1/libraries", s.libraries)
	api.HandleFunc("/api/v1/catalog/rebuild", s.rebuildCatalog)
	api.HandleFunc("/api/v1/admin/libraries", s.replaceLibraries)
	api.HandleFunc("/api/v1/games", s.games)
	api.HandleFunc("/api/v1/files/", s.file)

	root := http.NewServeMux()
	root.HandleFunc("/", s.adminRoot)
	root.HandleFunc("/admin", s.adminPage)
	root.HandleFunc("/admin/", s.adminPage)
	root.Handle("/api/", s.auth(api))
	return root
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
		"capabilities": map[string]any{
			"byte_ranges":               true,
			"anchored_virtual_sidecars": true,
			"catalog_discovery":         true,
			"catalog_etag":              true,
			"catalog_rebuild":           s.token != "",
			"library_editing":           s.token != "" && s.configPath != "",
			"transport_metrics":          true,
			"transport_metrics_reset":    s.token != "",
			"web_admin":                  true,
		},
	})
}

func (s *Server) transportMetrics(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		methodNotAllowed(w, http.MethodGet)
		return
	}

	uptime := time.Since(s.started)
	if uptime < 0 {
		uptime = 0
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"uptime_seconds":     uint64(uptime / time.Second),
		"file_get_requests":  s.metrics.fileGetRequests.Load(),
		"file_head_requests": s.metrics.fileHeadRequests.Load(),
		"range_requests":     s.metrics.rangeRequests.Load(),
		"full_get_requests":  s.metrics.fullGetRequests.Load(),
		"sidecar_requests":              s.metrics.sidecarRequests.Load(),
		"bytes_served":                  s.metrics.bytesServed.Load(),
		"range_bytes_served":            s.metrics.rangeBytesServed.Load(),
		"full_get_bytes_served":         s.metrics.fullGetBytesServed.Load(),
		"file_request_duration_us_total": s.metrics.fileDurationMicros.Load(),
		"file_request_duration_us_max":   s.metrics.fileDurationMicrosMax.Load(),
		"not_found":                     s.metrics.notFound.Load(),
		"errors":                        s.metrics.errors.Load(),
	})
}

func (s *Server) resetTransportMetrics(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		methodNotAllowed(w, http.MethodPost)
		return
	}
	if s.token == "" {
		writeJSON(w, http.StatusForbidden, map[string]string{
			"error": "transport metrics reset is disabled without a bearer token",
		})
		return
	}

	s.metrics.reset()
	writeJSON(w, http.StatusOK, map[string]any{
		"ok": true,
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

func (s *Server) replaceLibraries(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPut {
		methodNotAllowed(w, http.MethodPut)
		return
	}
	if s.token == "" || s.configPath == "" {
		writeJSON(w, http.StatusForbidden, map[string]string{
			"error": "library editing is disabled without managed config and bearer token",
		})
		return
	}

	var request struct {
		Libraries []config.Library `json:"libraries"`
	}
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&request); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid library configuration"})
		return
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid library configuration"})
		return
	}

	s.adminMu.Lock()
	defer s.adminMu.Unlock()

	next := s.cfg
	libraries, err := config.NormalizeLibraries(request.Libraries)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid library configuration"})
		return
	}
	next.Libraries = libraries

	// Build the complete replacement catalog before mutating persistent or live
	// state. A bad path or scan therefore cannot partially replace a working
	// configuration.
	prepared, err := catalog.New(next)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "library scan failed"})
		return
	}
	if err := config.Save(s.configPath, next); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "save configuration failed"})
		return
	}

	s.catalog.ReplaceFrom(prepared)
	s.cfg = next

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
type metricResponseWriter struct {
	http.ResponseWriter
	status int
	bytes  uint64
}

func (w *metricResponseWriter) WriteHeader(status int) {
	if w.status != 0 {
		return
	}
	w.status = status
	w.ResponseWriter.WriteHeader(status)
}

func (w *metricResponseWriter) Write(p []byte) (int, error) {
	if w.status == 0 {
		w.status = http.StatusOK
	}
	n, err := w.ResponseWriter.Write(p)
	w.bytes += uint64(n)
	return n, err
}

func (s *Server) file(w http.ResponseWriter, r *http.Request) {
	mw := &metricResponseWriter{ResponseWriter: w}
	started := time.Now()
	defer func() {
		s.metrics.bytesServed.Add(mw.bytes)
		if r.Method == http.MethodGet {
			if r.Header.Get("Range") != "" {
				s.metrics.rangeBytesServed.Add(mw.bytes)
			} else {
				s.metrics.fullGetBytesServed.Add(mw.bytes)
			}
		}
		if r.Method == http.MethodGet || r.Method == http.MethodHead {
			elapsed := time.Since(started)
			if elapsed < 0 {
				elapsed = 0
			}
			micros := uint64(elapsed / time.Microsecond)
			s.metrics.fileDurationMicros.Add(micros)
			atomicMax(&s.metrics.fileDurationMicrosMax, micros)
		}
		if mw.status == http.StatusNotFound {
			s.metrics.notFound.Add(1)
		}
		if mw.status >= 400 {
			s.metrics.errors.Add(1)
		}
	}()

	if r.Method == http.MethodGet {
		s.metrics.fileGetRequests.Add(1)
		if r.Header.Get("Range") != "" {
			s.metrics.rangeRequests.Add(1)
		} else {
			s.metrics.fullGetRequests.Add(1)
		}
	} else if r.Method == http.MethodHead {
		s.metrics.fileHeadRequests.Add(1)
	}

	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		mw.Header().Set("Allow", "GET, HEAD")
		writeJSON(mw, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}

	id := strings.TrimPrefix(r.URL.Path, "/api/v1/files/")
	if id == "" || strings.Contains(id, "/") || strings.Contains(id, "\\") {
		http.NotFound(mw, r)
		return
	}

	virtualPath := r.URL.Query().Get("path")
	if virtualPath != "" {
		s.metrics.sidecarRequests.Add(1)
	}
	f, entry, err := s.catalog.OpenVirtual(id, virtualPath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			http.NotFound(mw, r)
			return
		}
		writeJSON(mw, http.StatusInternalServerError, map[string]string{"error": "open file failed"})
		return
	}
	defer f.Close()

	mw.Header().Set("Content-Type", "application/octet-stream")
	mw.Header().Set("Accept-Ranges", "bytes")
	mw.Header().Set("ETag", entry.ETag)
	mw.Header().Set("X-Emu-System", entry.System)
	mw.Header().Set("X-Emu-Library", entry.Library)
	if virtualPath != "" {
		mw.Header().Set("X-Emu-Relative-Path", entry.RelativePath)
	}

	http.ServeContent(mw, r, entry.Name, entry.ModifiedAt, f)
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
