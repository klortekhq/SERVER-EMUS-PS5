package setup

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/klortekhq/server-emus-ps5/internal/config"
)

func TestWizardCreatesPortableConfig(t *testing.T) {
	root := t.TempDir()
	configPath := filepath.Join(t.TempDir(), "config.json")

	input := strings.Join([]string{
		"ps1",
		"My PS1",
		root,
		"y",
		"",
		"8787",
		"",
	}, "\n")

	var out bytes.Buffer
	if err := Run(strings.NewReader(input), &out, configPath); err != nil {
		t.Fatal(err)
	}

	cfg, err := config.Load(configPath)
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.Libraries) != 1 || cfg.Libraries[0].System != "ps1" {
		t.Fatalf("unexpected config: %+v", cfg)
	}
	if cfg.Libraries[0].Path != root {
		want, _ := filepath.Abs(root)
		if cfg.Libraries[0].Path != want {
			t.Fatalf("path=%q want %q", cfg.Libraries[0].Path, want)
		}
	}
	if _, err := os.Stat(configPath); err != nil {
		t.Fatal(err)
	}
}
