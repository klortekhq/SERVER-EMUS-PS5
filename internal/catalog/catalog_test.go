package catalog

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/klortekhq/server-emus-ps5/internal/config"
)

func TestCatalogFiltersAndHidesHostPaths(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "game.chd"), []byte("0123456789"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "ignore.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	cat, err := New(config.Config{Libraries: []config.Library{{
		Name: "PS1", System: "ps1", Path: root, Recursive: true, Extensions: []string{".chd"},
	}}})
	if err != nil {
		t.Fatal(err)
	}
	entries := cat.Entries("ps1")
	if len(entries) != 1 {
		t.Fatalf("got %d entries, want 1", len(entries))
	}
	if entries[0].RelativePath != "game.chd" {
		t.Fatalf("unexpected relative path: %q", entries[0].RelativePath)
	}
	if entries[0].ID == "" || entries[0].ETag == "" {
		t.Fatal("missing stable metadata")
	}
	if entries[0].HostPath == "" {
		t.Fatal("internal host path should exist inside catalog")
	}
}
