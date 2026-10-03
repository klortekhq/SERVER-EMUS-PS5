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

func TestOpenRegularWithinRootRejectsExternalOpenedHandle(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	secret := filepath.Join(outside, "secret.bin")
	if err := os.WriteFile(secret, []byte("outside"), 0o644); err != nil {
		t.Fatal(err)
	}

	resolvedRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		t.Fatal(err)
	}
	file, _, _, err := openRegularWithinRoot(resolvedRoot, secret)
	if file != nil {
		file.Close()
	}
	if err == nil {
		t.Fatal("opened external handle was accepted inside configured library")
	}
}

func TestCatalogOpenRejectsEntryRetargetedOutsideAfterScan(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()

	game := filepath.Join(root, "game.iso")
	if err := os.WriteFile(game, []byte("inside"), 0o644); err != nil {
		t.Fatal(err)
	}
	secret := filepath.Join(outside, "secret.iso")
	if err := os.WriteFile(secret, []byte("outside"), 0o644); err != nil {
		t.Fatal(err)
	}

	cat, err := New(config.Config{Libraries: []config.Library{{
		Name: "PS2", System: "ps2", Path: root, Recursive: true, Extensions: []string{".iso"},
	}}})
	if err != nil {
		t.Fatal(err)
	}
	entries := cat.Entries("ps2")
	if len(entries) != 1 {
		t.Fatalf("catalog entries=%d want 1", len(entries))
	}

	if err := os.Remove(game); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(secret, game); err != nil {
		t.Skipf("symlink retarget test unavailable: %v", err)
	}

	file, _, err := cat.Open(entries[0].ID)
	if file != nil {
		file.Close()
	}
	if err == nil {
		t.Fatal("catalog opened a path retargeted outside the configured library")
	}
}

func TestResolvedSidecarOpenRejectsRetargetBetweenResolveAndOpen(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	gameDir := filepath.Join(root, "game")
	if err := os.MkdirAll(gameDir, 0o755); err != nil {
		t.Fatal(err)
	}
	cuePath := filepath.Join(gameDir, "disc.cue")
	trackPath := filepath.Join(gameDir, "track.bin")
	if err := os.WriteFile(cuePath, []byte("FILE \"track.bin\" BINARY\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(trackPath, []byte("inside-track"), 0o644); err != nil {
		t.Fatal(err)
	}
	secret := filepath.Join(outside, "secret.bin")
	if err := os.WriteFile(secret, []byte("outside-track"), 0o644); err != nil {
		t.Fatal(err)
	}

	cat, err := New(config.Config{Libraries: []config.Library{{
		Name: "PS1", System: "ps1", Path: root, Recursive: true, Extensions: []string{".cue"},
	}}})
	if err != nil {
		t.Fatal(err)
	}
	entries := cat.Entries("ps1")
	if len(entries) != 1 {
		t.Fatalf("catalog entries=%d want 1", len(entries))
	}

	resolved, ok := cat.ResolveFromAnchor(entries[0].ID, "track.bin")
	if !ok {
		t.Fatal("sidecar resolution failed before retarget")
	}
	if err := os.Remove(trackPath); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(secret, trackPath); err != nil {
		t.Skipf("symlink retarget test unavailable: %v", err)
	}

	file, _, _, err := openRegularWithinRoot(resolved.LibraryRoot, resolved.HostPath)
	if file != nil {
		file.Close()
	}
	if err == nil {
		t.Fatal("resolved sidecar escaped the configured library after retarget")
	}
}
