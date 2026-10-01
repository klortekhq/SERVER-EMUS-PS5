package catalog

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/klortekhq/server-emus-ps5/internal/config"
)

func TestCatalogRejectsGameSymlinkEscapingLibrary(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()

	if err := os.WriteFile(filepath.Join(root, "inside.iso"), []byte("inside"), 0o644); err != nil {
		t.Fatal(err)
	}
	secret := filepath.Join(outside, "secret.iso")
	if err := os.WriteFile(secret, []byte("outside"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(secret, filepath.Join(root, "escape.iso")); err != nil {
		t.Skipf("symlink test unavailable: %v", err)
	}

	cat, err := New(config.Config{Libraries: []config.Library{{
		Name: "PS2", System: "ps2", Path: root, Recursive: true, Extensions: []string{".iso"},
	}}})
	if err != nil {
		t.Fatal(err)
	}

	entries := cat.Entries("ps2")
	if len(entries) != 1 || entries[0].Name != "inside.iso" {
		t.Fatalf("outside symlink entered catalog: %+v", entries)
	}
}

func TestCatalogAllowsContainedGameSymlinkAndOpensResolvedTarget(t *testing.T) {
	root := t.TempDir()
	targetDir := filepath.Join(root, "real")
	if err := os.MkdirAll(targetDir, 0o755); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(targetDir, "game.iso")
	if err := os.WriteFile(target, []byte("contained"), 0o644); err != nil {
		t.Fatal(err)
	}
	alias := filepath.Join(root, "alias.iso")
	if err := os.Symlink(target, alias); err != nil {
		t.Skipf("symlink test unavailable: %v", err)
	}

	cat, err := New(config.Config{Libraries: []config.Library{{
		Name: "PS2", System: "ps2", Path: root, Recursive: true, Extensions: []string{".iso"},
	}}})
	if err != nil {
		t.Fatal(err)
	}

	var aliasEntry Entry
	for _, entry := range cat.Entries("ps2") {
		if entry.Name == "alias.iso" {
			aliasEntry = entry
			break
		}
	}
	if aliasEntry.ID == "" {
		t.Fatal("contained symlink was not catalogued")
	}

	file, opened, err := cat.Open(aliasEntry.ID)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	expectedTarget, err := filepath.EvalSymlinks(target)
	if err != nil {
		t.Fatal(err)
	}
	if opened.HostPath != expectedTarget {
		t.Fatalf("host path was not resolved: got %q want %q", opened.HostPath, expectedTarget)
	}
	body := make([]byte, len("contained"))
	if _, err := file.Read(body); err != nil {
		t.Fatal(err)
	}
	if string(body) != "contained" {
		t.Fatalf("unexpected opened content: %q", body)
	}
}
