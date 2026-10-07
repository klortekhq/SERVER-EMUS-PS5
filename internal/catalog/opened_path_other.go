//go:build !linux && !windows

package catalog

import (
	"fmt"
	"os"
	"path/filepath"
)

func openedFilePath(file *os.File, candidate string) (string, error) {
	resolved, err := filepath.EvalSymlinks(candidate)
	if err != nil {
		return "", err
	}
	openedInfo, err := file.Stat()
	if err != nil {
		return "", err
	}
	currentInfo, err := os.Stat(resolved)
	if err != nil {
		return "", err
	}
	if !os.SameFile(openedInfo, currentInfo) {
		return "", fmt.Errorf("opened file changed during validation")
	}
	return filepath.Clean(resolved), nil
}
