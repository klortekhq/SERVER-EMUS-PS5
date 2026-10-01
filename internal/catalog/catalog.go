package catalog

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/klortekhq/server-emus-ps5/internal/config"
)

type Entry struct {
	ID           string    `json:"id"`
	Library      string    `json:"library"`
	System       string    `json:"system"`
	Name         string    `json:"name"`
	RelativePath string    `json:"relative_path"`
	Size         int64     `json:"size"`
	ModifiedAt   time.Time `json:"modified_at"`
	ETag         string    `json:"etag"`
	HostPath     string    `json:"-"`
	LibraryRoot  string    `json:"-"`
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
	mu      sync.RWMutex
	cfg     config.Config
	entries  map[string]Entry
	systems  map[string]int
	revision string
}

func New(cfg config.Config) (*Catalog, error) {
	c := &Catalog{cfg: cfg}
	if err := c.Rebuild(); err != nil {
		return nil, err
	}
	return c, nil
}

func (c *Catalog) Rebuild() error {
	entries := make(map[string]Entry)
	systems := make(map[string]int)

	for _, lib := range c.cfg.Libraries {
		if err := scanLibrary(lib, entries, systems); err != nil {
			return err
		}
	}
	revision := catalogRevision(c.cfg, entries)

	c.mu.Lock()
	c.entries = entries
	c.systems = systems
	c.revision = revision
	c.mu.Unlock()
	return nil
}

func scanLibrary(
	lib config.Library,
	entries map[string]Entry,
	systems map[string]int,
) error {
	allowed := make(map[string]struct{}, len(lib.Extensions))
	for _, ext := range lib.Extensions {
		if ext != "" {
			allowed[strings.ToLower(ext)] = struct{}{}
		}
	}

	return filepath.WalkDir(lib.Path, func(filePath string, d fs.DirEntry, err error) error {
		if err != nil {
			return fmt.Errorf("scan %q: %w", lib.Name, err)
		}
		if filePath == lib.Path {
			return nil
		}
		if d.IsDir() {
			if !lib.Recursive {
				return filepath.SkipDir
			}
			return nil
		}
		if !d.Type().IsRegular() {
			info, infoErr := d.Info()
			if infoErr != nil || !info.Mode().IsRegular() {
				return nil
			}
		}

		if len(allowed) != 0 {
			if _, ok := allowed[strings.ToLower(filepath.Ext(d.Name()))]; !ok {
				return nil
			}
		}

		info, err := d.Info()
		if err != nil {
			return fmt.Errorf("stat %q: %w", filePath, err)
		}
		rel, err := filepath.Rel(lib.Path, filePath)
		if err != nil {
			return fmt.Errorf("relative path %q: %w", filePath, err)
		}
		rel = filepath.ToSlash(rel)
		id := stableID(lib.Name, lib.System, rel)
		etag := metadataETag(info.Size(), info.ModTime())

		entries[id] = Entry{
			ID:           id,
			Library:      lib.Name,
			System:       lib.System,
			Name:         d.Name(),
			RelativePath: rel,
			Size:         info.Size(),
			ModifiedAt:   info.ModTime().UTC(),
			ETag:         etag,
			HostPath:     filePath,
			LibraryRoot:  lib.Path,
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
	}

	sum := h.Sum(nil)
	return `"catalog-` + hex.EncodeToString(sum[:16]) + `"`
}

func (c *Catalog) Revision() string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.revision
}

func stableID(library, system, relative string) string {
	sum := sha256.Sum256([]byte(library + "\x00" + system + "\x00" + filepath.ToSlash(relative)))
	return hex.EncodeToString(sum[:])
}

func metadataETag(size int64, mod time.Time) string {
	sum := sha256.Sum256([]byte(fmt.Sprintf("%d:%d", size, mod.UnixNano())))
	return `"` + hex.EncodeToString(sum[:16]) + `"`
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

	f, err := os.Open(entry.HostPath)
	if err != nil {
		return nil, Entry{}, err
	}
	info, err := f.Stat()
	if err != nil {
		f.Close()
		return nil, Entry{}, err
	}
	if !info.Mode().IsRegular() {
		f.Close()
		return nil, Entry{}, fmt.Errorf("catalog entry is no longer a regular file")
	}

	// Refresh response metadata from the opened file descriptor. A file may
	// change between periodic catalog rescans; stale ETags would make If-Range
	// semantics unsafe for a running emulator.
	entry.Size = info.Size()
	entry.ModifiedAt = info.ModTime().UTC()
	entry.ETag = metadataETag(info.Size(), info.ModTime())
	return f, entry, nil
}
