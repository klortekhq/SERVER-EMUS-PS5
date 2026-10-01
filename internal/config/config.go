package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

type Library struct {
	Name       string   `json:"name"`
	System     string   `json:"system"`
	Path       string   `json:"path"`
	Recursive  bool     `json:"recursive"`
	Extensions []string `json:"extensions,omitempty"`
}

type Config struct {
	Listen    string    `json:"listen"`
	Token     string    `json:"token,omitempty"`
	Libraries []Library `json:"libraries"`
}

func Load(path string) (Config, error) {
	var cfg Config

	data, err := os.ReadFile(path)
	if err != nil {
		return cfg, fmt.Errorf("read config: %w", err)
	}
	if err := json.Unmarshal(data, &cfg); err != nil {
		return cfg, fmt.Errorf("parse config: %w", err)
	}
	if strings.TrimSpace(cfg.Listen) == "" {
		cfg.Listen = "0.0.0.0:8787"
	}
	if len(cfg.Libraries) == 0 {
		return cfg, errors.New("at least one library is required")
	}

	seen := make(map[string]struct{}, len(cfg.Libraries))
	for i := range cfg.Libraries {
		lib := &cfg.Libraries[i]
		lib.Name = strings.TrimSpace(lib.Name)
		lib.System = strings.ToLower(strings.TrimSpace(lib.System))
		lib.Path = strings.TrimSpace(lib.Path)
		if lib.Name == "" {
			return cfg, fmt.Errorf("library %d: name is required", i)
		}
		if lib.System == "" {
			return cfg, fmt.Errorf("library %q: system is required", lib.Name)
		}
		if lib.Path == "" {
			return cfg, fmt.Errorf("library %q: path is required", lib.Name)
		}
		if _, ok := seen[lib.Name]; ok {
			return cfg, fmt.Errorf("duplicate library name %q", lib.Name)
		}
		seen[lib.Name] = struct{}{}

		abs, err := filepath.Abs(lib.Path)
		if err != nil {
			return cfg, fmt.Errorf("library %q: resolve path: %w", lib.Name, err)
		}
		info, err := os.Stat(abs)
		if err != nil {
			return cfg, fmt.Errorf("library %q: stat path: %w", lib.Name, err)
		}
		if !info.IsDir() {
			return cfg, fmt.Errorf("library %q: path is not a directory", lib.Name)
		}
		lib.Path = filepath.Clean(abs)

		for j, ext := range lib.Extensions {
			ext = strings.ToLower(strings.TrimSpace(ext))
			if ext != "" && !strings.HasPrefix(ext, ".") {
				ext = "." + ext
			}
			lib.Extensions[j] = ext
		}
	}

	return cfg, nil
}
