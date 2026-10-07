package catalog

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/url"
	"os"
	pathpkg "path"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/klortekhq/server-emus-ps5/internal/config"
)

type Entry struct {
	ID           string        `json:"id"`
	Library      string        `json:"library"`
	System       string        `json:"system"`
	Name         string        `json:"name"`
	RelativePath string        `json:"relative_path"`
	Size         int64         `json:"size"`
	ModifiedAt   time.Time     `json:"modified_at"`
	ETag         string        `json:"etag"`
	Metadata     *GameMetadata `json:"metadata,omitempty"`
	HostPath     string        `json:"-"`
	LibraryRoot  string        `json:"-"`
}

// GameMetadata is an optional, user-maintained sidecar for a catalogued game.
// It contains references and resource paths only; the server never downloads,
// activates, or executes cheat files or artwork automatically.
type GameMetadata struct {
	Title       string            `json:"title,omitempty"`
	Identifiers []GameIdentifier  `json:"identifiers,omitempty"`
	Region      string            `json:"region,omitempty"`
	Developer   string            `json:"developer,omitempty"`
	Publisher   string            `json:"publisher,omitempty"`
	ReleaseDate string            `json:"release_date,omitempty"`
	Genres      []string          `json:"genres,omitempty"`
	References  []MetadataLink    `json:"references,omitempty"`
	Artwork     []ArtworkResource `json:"artwork,omitempty"`
	Cheats      []CheatResource   `json:"cheats,omitempty"`
}

type GameIdentifier struct {
	Kind     string `json:"kind"`
	Value    string `json:"value"`
	Region   string `json:"region,omitempty"`
	Revision string `json:"revision,omitempty"`
}

type MetadataLink struct {
	Label string `json:"label"`
	URL   string `json:"url"`
}

type ArtworkResource struct {
	Kind        string `json:"kind"`
	Path        string `json:"path,omitempty"`
	URL         string `json:"url,omitempty"`
	SHA256      string `json:"sha256,omitempty"`
	License     string `json:"license,omitempty"`
	Attribution string `json:"attribution,omitempty"`
}

type CheatResource struct {
	Format      string `json:"format"`
	Path        string `json:"path,omitempty"`
	URL         string `json:"url,omitempty"`
	GameVersion string `json:"game_version,omitempty"`
	Emulator    string `json:"emulator,omitempty"`
	SHA256      string `json:"sha256,omitempty"`
	License     string `json:"license,omitempty"`
	Attribution string `json:"attribution,omitempty"`
}

type SystemStat struct {
	System string `json:"system"`
	Files  int    `json:"files"`
}

type LibraryStat struct {
	Name       string   `json:"name"`
	System     string   `json:"system"`
	Recursive  bool     `json:"recursive"`
	Extensions []string `json:"extensions,omitempty"`
	Files      int      `json:"files"`
}

type Catalog struct {
	mu               sync.RWMutex
	cfg              config.Config
	configGeneration uint64
	entries          map[string]Entry
	systems          map[string]int
	revision         string
}

func New(cfg config.Config) (*Catalog, error) {
	c := &Catalog{cfg: cfg}
	if err := c.Rebuild(); err != nil {
		return nil, err
	}
	return c, nil
}

func (c *Catalog) Rebuild() error {
	// Rescans run in the background while administrators can replace the
	// configured libraries. Build from a stable config snapshot and discard
	// the result if a replacement won the race before publication.
	c.mu.RLock()
	cfg := c.cfg
	generation := c.configGeneration
	c.mu.RUnlock()

	entries := make(map[string]Entry)
	systems := make(map[string]int)

	for _, lib := range cfg.Libraries {
		if err := scanLibrary(lib, entries, systems); err != nil {
			return err
		}
	}
	revision := catalogRevision(cfg, entries)

	c.mu.Lock()
	defer c.mu.Unlock()
	if c.configGeneration != generation {
		return errStaleRebuild
	}
	c.entries = entries
	c.systems = systems
	c.revision = revision
	return nil
}

var errStaleRebuild = errors.New("catalog configuration changed during rebuild; stale scan discarded")

func scanLibrary(
	lib config.Library,
	entries map[string]Entry,
	systems map[string]int,
) error {
	resolvedRoot, err := filepath.EvalSymlinks(lib.Path)
	if err != nil {
		return fmt.Errorf("resolve library %q: %w", lib.Name, err)
	}

	allowed := make(map[string]struct{}, len(lib.Extensions))
	for _, ext := range lib.Extensions {
		if ext != "" {
			allowed[strings.ToLower(ext)] = struct{}{}
		}
	}

	return filepath.WalkDir(resolvedRoot, func(filePath string, d fs.DirEntry, err error) error {
		if err != nil {
			return fmt.Errorf("scan %q: %w", lib.Name, err)
		}
		if filePath == resolvedRoot {
			return nil
		}
		if d.IsDir() {
			if !lib.Recursive {
				return filepath.SkipDir
			}
			return nil
		}
		if len(allowed) != 0 {
			if _, ok := allowed[strings.ToLower(filepath.Ext(d.Name()))]; !ok {
				return nil
			}
		}

		resolvedPath, ok := resolveExistingWithinRoot(resolvedRoot, filePath)
		if !ok {
			// A regular-looking symlink may target a file outside the configured
			// library. Keep it out of the catalog instead of turning the server
			// into an arbitrary file reader.
			return nil
		}
		info, err := os.Stat(resolvedPath)
		if err != nil {
			return fmt.Errorf("stat %q: %w", filePath, err)
		}
		if !info.Mode().IsRegular() {
			return nil
		}
		rel, err := filepath.Rel(resolvedRoot, filePath)
		if err != nil {
			return fmt.Errorf("relative path %q: %w", filePath, err)
		}
		rel = filepath.ToSlash(rel)
		id := stableID(lib.Name, lib.System, rel)
		etag := metadataETag(info.Size(), info.ModTime())
		metadata, err := loadGameMetadata(resolvedRoot, filePath)
		if err != nil {
			return fmt.Errorf("metadata for %q: %w", rel, err)
		}

		entries[id] = Entry{
			ID:           id,
			Library:      lib.Name,
			System:       lib.System,
			Name:         d.Name(),
			RelativePath: rel,
			Size:         info.Size(),
			ModifiedAt:   info.ModTime().UTC(),
			ETag:         etag,
			Metadata:     metadata,
			HostPath:     resolvedPath,
			LibraryRoot:  resolvedRoot,
		}
		systems[lib.System]++
		return nil
	})
}

func catalogRevision(cfg config.Config, entries map[string]Entry) string {
	h := sha256.New()

	libs := append([]config.Library(nil), cfg.Libraries...)
	sort.Slice(libs, func(i, j int) bool {
		if libs[i].System != libs[j].System {
			return libs[i].System < libs[j].System
		}
		return strings.ToLower(libs[i].Name) < strings.ToLower(libs[j].Name)
	})
	for _, lib := range libs {
		fmt.Fprintf(h, "L\x00%s\x00%s\x00%t\x00", lib.Name, lib.System, lib.Recursive)
		exts := append([]string(nil), lib.Extensions...)
		sort.Strings(exts)
		for _, ext := range exts {
			fmt.Fprintf(h, "%s\x00", ext)
		}
	}

	ids := make([]string, 0, len(entries))
	for id := range entries {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		entry := entries[id]
		fmt.Fprintf(
			h, "E\x00%s\x00%d\x00%d\x00",
			id, entry.Size, entry.ModifiedAt.UnixNano(),
		)
		if metadata, err := json.Marshal(entry.Metadata); err == nil {
			fmt.Fprintf(h, "M\x00%s\x00", metadata)
		}
	}

	sum := h.Sum(nil)
	return `"catalog-` + hex.EncodeToString(sum[:16]) + `"`
}

func (c *Catalog) Revision() string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.revision
}

func (c *Catalog) ReplaceFrom(next *Catalog) {
	if next == nil || next == c {
		return
	}

	next.mu.RLock()
	cfg := next.cfg
	entries := make(map[string]Entry, len(next.entries))
	for id, entry := range next.entries {
		entries[id] = entry
	}
	systems := make(map[string]int, len(next.systems))
	for system, count := range next.systems {
		systems[system] = count
	}
	revision := next.revision
	next.mu.RUnlock()

	c.mu.Lock()
	c.cfg = cfg
	c.configGeneration++
	c.entries = entries
	c.systems = systems
	c.revision = revision
	c.mu.Unlock()
}

func stableID(library, system, relative string) string {
	sum := sha256.Sum256([]byte(library + "\x00" + system + "\x00" + filepath.ToSlash(relative)))
	return hex.EncodeToString(sum[:])
}

func metadataETag(size int64, mod time.Time) string {
	sum := sha256.Sum256([]byte(fmt.Sprintf("%d:%d", size, mod.UnixNano())))
	return `"` + hex.EncodeToString(sum[:16]) + `"`
}

func loadGameMetadata(root, gamePath string) (*GameMetadata, error) {
	path := gamePath + ".emus.json"
	info, err := os.Lstat(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	rel, err := filepath.Rel(root, path)
	if err != nil || filepath.IsAbs(rel) || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return nil, fmt.Errorf("sidecar escapes configured library")
	}
	if info.Mode()&os.ModeSymlink != 0 {
		resolved, resolveErr := filepath.EvalSymlinks(path)
		if resolveErr != nil {
			return nil, resolveErr
		}
		resolvedRoot, resolveErr := filepath.EvalSymlinks(root)
		if resolveErr != nil {
			return nil, resolveErr
		}
		rel, resolveErr := filepath.Rel(resolvedRoot, resolved)
		if resolveErr != nil || filepath.IsAbs(rel) || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			return nil, fmt.Errorf("sidecar escapes configured library")
		}
		path = resolved
		info, err = os.Stat(path)
		if err != nil {
			return nil, err
		}
	}
	if !info.Mode().IsRegular() || info.Size() > 64*1024 {
		return nil, fmt.Errorf("sidecar must be a regular file no larger than 64 KiB")
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	decoder := json.NewDecoder(io.LimitReader(f, 64*1024+1))
	decoder.DisallowUnknownFields()
	var metadata GameMetadata
	if err := decoder.Decode(&metadata); err != nil {
		return nil, err
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		return nil, fmt.Errorf("sidecar must contain a single JSON object")
	}
	if err := validateGameMetadata(&metadata); err != nil {
		return nil, err
	}
	return &metadata, nil
}

func validateGameMetadata(m *GameMetadata) error {
	if len(m.Title) > 512 || len(m.Region) > 64 || len(m.Developer) > 256 || len(m.Publisher) > 256 || len(m.ReleaseDate) > 32 {
		return fmt.Errorf("text field exceeds its size limit")
	}
	if len(m.Identifiers) > 32 || len(m.Genres) > 32 || len(m.References) > 64 || len(m.Artwork) > 64 || len(m.Cheats) > 128 {
		return fmt.Errorf("metadata list exceeds its size limit")
	}
	for _, id := range m.Identifiers {
		if id.Kind == "" || id.Value == "" || len(id.Kind) > 64 || len(id.Value) > 128 || len(id.Region) > 64 || len(id.Revision) > 64 {
			return fmt.Errorf("invalid platform identifier")
		}
	}
	for _, genre := range m.Genres {
		if genre == "" || len(genre) > 64 {
			return fmt.Errorf("invalid genre")
		}
	}
	for _, link := range m.References {
		if len(link.Label) > 128 || !validHTTPSURL(link.URL) {
			return fmt.Errorf("references must have a label and an HTTPS URL")
		}
	}
	for _, art := range m.Artwork {
		if art.Kind == "" || len(art.Kind) > 64 || !validResourceLocation(art.Path, art.URL) || !validSHA256(art.SHA256) || len(art.License) > 160 || len(art.Attribution) > 256 {
			return fmt.Errorf("invalid artwork resource")
		}
	}
	for _, cheat := range m.Cheats {
		if cheat.Format == "" || len(cheat.Format) > 64 || !validResourceLocation(cheat.Path, cheat.URL) || !validSHA256(cheat.SHA256) || len(cheat.GameVersion) > 128 || len(cheat.Emulator) > 128 || len(cheat.License) > 160 || len(cheat.Attribution) > 256 {
			return fmt.Errorf("invalid cheat resource")
		}
	}
	return nil
}

func validSHA256(value string) bool {
	if value == "" {
		return true
	}
	if len(value) != sha256.Size*2 {
		return false
	}
	_, err := hex.DecodeString(value)
	return err == nil
}

func validResourceLocation(path, url string) bool {
	if (path == "") == (url == "") {
		return false
	}
	if url != "" {
		return validHTTPSURL(url)
	}
	path = strings.ReplaceAll(path, "\\", "/")
	clean := pathpkg.Clean(path)
	return !strings.ContainsRune(path, 0) && !strings.Contains(path, ":") && !strings.HasPrefix(path, "/") && !filepath.IsAbs(path) && clean != "." && clean != ".." && !strings.HasPrefix(clean, "../")
}

func validHTTPSURL(raw string) bool {
	if len(raw) > 2048 {
		return false
	}
	parsed, err := url.ParseRequestURI(raw)
	return err == nil && strings.EqualFold(parsed.Scheme, "https") && parsed.Host != "" && parsed.User == nil
}

func (c *Catalog) Get(id string) (Entry, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	entry, ok := c.entries[id]
	return entry, ok
}

// ResolveFromAnchor resolves a virtual path relative to the directory that
// contains the catalog entry identified by anchorID. The resolved file must
// still exist inside the same configured library.
//
// This lets descriptor formats such as CUE/CCD/TOC/M3U keep using ordinary
// relative sidecar paths while the PS5 only carries one opaque catalog ID.
// Sidecars do not need to be launchable/catalogued extensions; they are
// resolved from disk only after proving the target remains inside the same
// configured library (including after symlink resolution).
func (c *Catalog) ResolveFromAnchor(anchorID, virtualPath string) (Entry, bool) {
	anchor, ok := c.Get(anchorID)
	if !ok {
		return Entry{}, false
	}
	return resolveFromAnchor(anchor, virtualPath)
}

func resolveFromAnchor(anchor Entry, virtualPath string) (Entry, bool) {
	if virtualPath == "" {
		return anchor, true
	}
	if strings.IndexByte(virtualPath, 0) >= 0 {
		return Entry{}, false
	}

	// Descriptor paths use slash semantics even when the server runs on
	// Windows. Convert them only after rejecting absolute virtual paths.
	virtualPath = strings.ReplaceAll(virtualPath, "\\", "/")
	if strings.HasPrefix(virtualPath, "/") {
		return Entry{}, false
	}

	root, err := filepath.EvalSymlinks(anchor.LibraryRoot)
	if err != nil {
		return Entry{}, false
	}
	target := filepath.Clean(filepath.Join(
		filepath.Dir(anchor.HostPath),
		filepath.FromSlash(virtualPath),
	))
	target, err = filepath.EvalSymlinks(target)
	if err != nil {
		return Entry{}, false
	}

	relative, err := filepath.Rel(root, target)
	if err != nil || filepath.IsAbs(relative) ||
		relative == ".." ||
		strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return Entry{}, false
	}

	info, err := os.Stat(target)
	if err != nil || !info.Mode().IsRegular() {
		return Entry{}, false
	}

	relative = filepath.ToSlash(relative)
	return Entry{
		ID:           stableID(anchor.Library, anchor.System, relative),
		Library:      anchor.Library,
		System:       anchor.System,
		Name:         filepath.Base(target),
		RelativePath: relative,
		Size:         info.Size(),
		ModifiedAt:   info.ModTime().UTC(),
		ETag:         metadataETag(info.Size(), info.ModTime()),
		HostPath:     target,
		LibraryRoot:  root,
	}, true
}

func (c *Catalog) Entries(system string) []Entry {
	system = strings.ToLower(strings.TrimSpace(system))
	c.mu.RLock()
	out := make([]Entry, 0, len(c.entries))
	for _, entry := range c.entries {
		if system == "" || entry.System == system {
			out = append(out, entry)
		}
	}
	c.mu.RUnlock()

	sort.Slice(out, func(i, j int) bool {
		if out[i].System != out[j].System {
			return out[i].System < out[j].System
		}
		if out[i].Library != out[j].Library {
			return out[i].Library < out[j].Library
		}
		return strings.ToLower(out[i].RelativePath) < strings.ToLower(out[j].RelativePath)
	})
	return out
}

func (c *Catalog) Libraries() []LibraryStat {
	c.mu.RLock()
	defer c.mu.RUnlock()

	counts := make(map[string]int, len(c.cfg.Libraries))
	for _, entry := range c.entries {
		counts[entry.Library]++
	}

	out := make([]LibraryStat, 0, len(c.cfg.Libraries))
	for _, lib := range c.cfg.Libraries {
		extensions := append([]string(nil), lib.Extensions...)
		out = append(out, LibraryStat{
			Name:       lib.Name,
			System:     lib.System,
			Recursive:  lib.Recursive,
			Extensions: extensions,
			Files:      counts[lib.Name],
		})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].System != out[j].System {
			return out[i].System < out[j].System
		}
		return strings.ToLower(out[i].Name) < strings.ToLower(out[j].Name)
	})
	return out
}

func (c *Catalog) Systems() []SystemStat {
	c.mu.RLock()
	out := make([]SystemStat, 0, len(c.systems))
	for system, count := range c.systems {
		out = append(out, SystemStat{System: system, Files: count})
	}
	c.mu.RUnlock()

	sort.Slice(out, func(i, j int) bool { return out[i].System < out[j].System })
	return out
}

func (c *Catalog) Open(id string) (*os.File, Entry, error) {
	return c.OpenVirtual(id, "")
}

func (c *Catalog) OpenVirtual(anchorID, virtualPath string) (*os.File, Entry, error) {
	entry, ok := c.ResolveFromAnchor(anchorID, virtualPath)
	if !ok {
		return nil, Entry{}, os.ErrNotExist
	}
	return openEntry(entry)
}

// OpenArtwork opens one locally declared artwork item while holding a stable
// catalog snapshot. External URLs are intentionally not fetched by the server.
func (c *Catalog) OpenArtwork(anchorID string, index int) (*os.File, Entry, ArtworkResource, error) {
	c.mu.RLock()
	defer c.mu.RUnlock()

	anchor, ok := c.entries[anchorID]
	if !ok || anchor.Metadata == nil || index < 0 || index >= len(anchor.Metadata.Artwork) {
		return nil, Entry{}, ArtworkResource{}, os.ErrNotExist
	}
	resource := anchor.Metadata.Artwork[index]
	if resource.Path == "" {
		return nil, Entry{}, ArtworkResource{}, os.ErrNotExist
	}
	entry, ok := resolveFromAnchor(anchor, resource.Path)
	if !ok {
		return nil, Entry{}, ArtworkResource{}, os.ErrNotExist
	}
	f, entry, err := openEntry(entry)
	if err != nil {
		return nil, Entry{}, ArtworkResource{}, err
	}
	return f, entry, resource, nil
}

func openEntry(entry Entry) (*os.File, Entry, error) {
	f, info, openedPath, err := openRegularWithinRoot(
		entry.LibraryRoot,
		entry.HostPath,
	)
	if err != nil {
		return nil, Entry{}, err
	}

	// Refresh response metadata from the opened file descriptor. A file may
	// change between periodic catalog rescans; stale ETags would make If-Range
	// semantics unsafe for a running emulator. HostPath also follows the object
	// actually opened so callers never retain a stale pre-open resolution.
	entry.HostPath = openedPath
	entry.Size = info.Size()
	entry.ModifiedAt = info.ModTime().UTC()
	entry.ETag = metadataETag(info.Size(), info.ModTime())
	return f, entry, nil
}
