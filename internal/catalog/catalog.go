package catalog

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io/fs"
	"os"
	"path"
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
}

type SystemStat struct {
	System string `json:"system"`
	Files  int    `json:"files"`
}

type Catalog struct {
	mu      sync.RWMutex
	cfg     config.Config
	entries map[string]Entry
	paths   map[string]string
	systems map[string]int
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
	paths := make(map[string]string)
	systems := make(map[string]int)

	for _, lib := range c.cfg.Libraries {
		if err := scanLibrary(lib, entries, paths, systems); err != nil {
			return err
		}
	}

	c.mu.Lock()
	c.entries = entries
	c.paths = paths
	c.systems = systems
	c.mu.Unlock()
	return nil
}

func scanLibrary(
	lib config.Library,
	entries map[string]Entry,
	paths map[string]string,
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
		}
		paths[pathKey(lib.Name, rel)] = id
		systems[lib.System]++
		return nil
	})
}

func pathKey(library, relative string) string {
	return library + "\x00" + filepath.ToSlash(relative)
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
func (c *Catalog) ResolveFromAnchor(anchorID, virtualPath string) (Entry, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()

	anchor, ok := c.entries[anchorID]
	if !ok {
		return Entry{}, false
	}
	if virtualPath == "" {
		return anchor, true
	}
	if strings.IndexByte(virtualPath, 0) >= 0 {
		return Entry{}, false
	}

	virtualPath = strings.ReplaceAll(virtualPath, "\\", "/")
	if path.IsAbs(virtualPath) {
		return Entry{}, false
	}

	base := path.Dir(anchor.RelativePath)
	resolved := path.Clean(path.Join(base, virtualPath))
	if resolved == "." || resolved == ".." || strings.HasPrefix(resolved, "../") {
		return Entry{}, false
	}

	id, ok := c.paths[pathKey(anchor.Library, resolved)]
	if !ok {
		return Entry{}, false
	}
	entry, ok := c.entries[id]
	return entry, ok
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
