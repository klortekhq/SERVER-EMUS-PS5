package catalog

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
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

func TestCatalogLoadsMetadataWithoutExposingHostPaths(t *testing.T) {
	root := t.TempDir()
	gamePath := filepath.Join(root, "game.iso")
	if err := os.WriteFile(gamePath, []byte("game"), 0o644); err != nil {
		t.Fatal(err)
	}
	metadata := `{
	  "title": "Example Game",
	  "identifiers": [{"kind":"disc_serial","value":"BLES12345","region":"Europe","revision":"1.00"}],
	  "references": [{"label":"Official page","url":"https://example.org/game"}],
	  "artwork": [{"kind":"box_front","path":"art/front.png","sha256":"0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef","license":"user-provided"}],
	  "cheats": [{"format":"libretro-cht","path":"cheats/game.cht","game_version":"1.00","emulator":"example-core","license":"CC-BY-SA-4.0","attribution":"community"}]
	}`
	if err := os.WriteFile(gamePath+".emus.json", []byte(metadata), 0o644); err != nil {
		t.Fatal(err)
	}

	cat, err := New(config.Config{Libraries: []config.Library{{
		Name: "PS3", System: "ps3", Path: root, Recursive: true, Extensions: []string{".iso"},
	}}})
	if err != nil {
		t.Fatal(err)
	}
	entries := cat.Entries("ps3")
	if len(entries) != 1 || entries[0].Metadata == nil {
		t.Fatalf("metadata was not attached to one game entry: %+v", entries)
	}
	got := entries[0].Metadata
	if got.Title != "Example Game" || len(got.Identifiers) != 1 || got.Identifiers[0].Value != "BLES12345" {
		t.Fatalf("unexpected identity metadata: %+v", got)
	}
	if len(got.Artwork) != 1 || got.Artwork[0].Path != "art/front.png" || len(got.Cheats) != 1 || got.Cheats[0].Path != "cheats/game.cht" {
		t.Fatalf("unexpected optional resources: %+v", got)
	}
	encoded, err := json.Marshal(entries[0])
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), root) || strings.Contains(string(encoded), gamePath) {
		t.Fatalf("host path leaked in public catalog entry: %s", encoded)
	}
	before := cat.Revision()
	if err := os.WriteFile(gamePath+".emus.json", []byte(strings.Replace(metadata, "Example Game", "Updated Name", 1)), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := cat.Rebuild(); err != nil {
		t.Fatal(err)
	}
	if cat.Revision() == before {
		t.Fatal("metadata changes must change the catalog ETag")
	}
}

func TestMetadataRejectsUnsafeResourcePathsAndURLs(t *testing.T) {
	for _, m := range []GameMetadata{
		{Artwork: []ArtworkResource{{Kind: "box", Path: "art/../../secret.png"}}},
		{Cheats: []CheatResource{{Format: "cht", URL: "http://example.org/cheats"}}},
		{Artwork: []ArtworkResource{{Kind: "box", Path: "art/front.png", SHA256: "not-a-hash"}}},
	} {
		if err := validateGameMetadata(&m); err == nil {
			t.Fatalf("unsafe metadata accepted: %+v", m)
		}
	}
}

func TestResolveFromAnchorKeepsSidecarsInsideLibrary(t *testing.T) {
	root := t.TempDir()
	gameDir := filepath.Join(root, "sets", "Ridge Racer")
	if err := os.MkdirAll(filepath.Join(gameDir, "tracks"), 0o755); err != nil {
		t.Fatal(err)
	}

	files := map[string]string{
		filepath.Join(gameDir, "Ridge Racer.cue"):       "FILE \"tracks/track01.bin\" BINARY\n",
		filepath.Join(gameDir, "tracks", "track01.bin"): "TRACK",
		filepath.Join(root, "sets", "cover.bin"):        "PARENT",
	}
	for name, body := range files {
		if err := os.WriteFile(name, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	cat, err := New(config.Config{Libraries: []config.Library{{
		Name:       "PS1",
		System:     "ps1",
		Path:       root,
		Recursive:  true,
		Extensions: []string{".cue"},
	}}})
	if err != nil {
		t.Fatal(err)
	}

	var cue Entry
	for _, entry := range cat.Entries("ps1") {
		if entry.Name == "Ridge Racer.cue" {
			cue = entry
			break
		}
	}
	if cue.ID == "" {
		t.Fatal("cue anchor not catalogued")
	}

	if got := len(cat.Entries("ps1")); got != 1 {
		t.Fatalf("sidecars leaked into launchable catalog: got %d entries", got)
	}

	track, ok := cat.ResolveFromAnchor(cue.ID, "tracks/track01.bin")
	if !ok || track.Name != "track01.bin" {
		t.Fatalf("sidecar resolution failed: ok=%v entry=%+v", ok, track)
	}

	parent, ok := cat.ResolveFromAnchor(cue.ID, "../cover.bin")
	if !ok || parent.Name != "cover.bin" {
		t.Fatalf("in-library parent resolution failed: ok=%v entry=%+v", ok, parent)
	}

	if _, ok := cat.ResolveFromAnchor(cue.ID, "../../../outside.bin"); ok {
		t.Fatal("anchor traversal escaped configured library")
	}
	if _, ok := cat.ResolveFromAnchor(cue.ID, "/etc/passwd"); ok {
		t.Fatal("absolute virtual path was accepted")
	}

	outside := t.TempDir()
	secret := filepath.Join(outside, "secret.bin")
	if err := os.WriteFile(secret, []byte("SECRET"), 0o644); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(gameDir, "escape.bin")
	if err := os.Symlink(secret, link); err != nil {
		t.Logf("symlink escape test skipped on this runner: %v", err)
		return
	}
	if _, ok := cat.ResolveFromAnchor(cue.ID, "escape.bin"); ok {
		t.Fatal("symlink escaped configured library")
	}
}
