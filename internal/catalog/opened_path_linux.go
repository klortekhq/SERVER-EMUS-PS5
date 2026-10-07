//go:build linux

package catalog

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

func openedFilePath(file *os.File, _ string) (string, error) {
	link := "/proc/self/fd/" + strconv.FormatUint(uint64(file.Fd()), 10)
	path, err := os.Readlink(link)
	if err != nil {
		return "", fmt.Errorf("resolve opened file descriptor: %w", err)
	}
	if strings.HasSuffix(path, " (deleted)") {
		return "", fmt.Errorf("opened file was replaced during validation")
	}
	if !filepath.IsAbs(path) {
		return "", fmt.Errorf("opened file descriptor resolved to a non-absolute path")
	}
	return filepath.Clean(path), nil
}
