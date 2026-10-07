//go:build windows

package catalog

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"unsafe"
)

var (
	kernel32DLL                   = syscall.NewLazyDLL("kernel32.dll")
	getFinalPathNameByHandleWProc = kernel32DLL.NewProc("GetFinalPathNameByHandleW")
)

func openedFilePath(file *os.File, _ string) (string, error) {
	buffer := make([]uint16, 512)
	for {
		length, _, callErr := getFinalPathNameByHandleWProc.Call(
			file.Fd(),
			uintptr(unsafe.Pointer(&buffer[0])),
			uintptr(len(buffer)),
			0,
		)
		if length == 0 {
			if errno, ok := callErr.(syscall.Errno); ok && errno != 0 {
				return "", fmt.Errorf("resolve opened Windows handle: %w", errno)
			}
			return "", fmt.Errorf("resolve opened Windows handle failed")
		}
		if length < uintptr(len(buffer)) {
			path := syscall.UTF16ToString(buffer[:length])
			return normalizeWindowsFinalPath(path)
		}
		buffer = make([]uint16, int(length)+1)
	}
}

func normalizeWindowsFinalPath(path string) (string, error) {
	if strings.HasPrefix(path, `\\?\UNC\`) {
		path = `\\` + strings.TrimPrefix(path, `\\?\UNC\`)
	} else {
		path = strings.TrimPrefix(path, `\\?\`)
	}
	if !filepath.IsAbs(path) {
		return "", fmt.Errorf("opened Windows handle resolved to a non-absolute path")
	}
	return filepath.Clean(path), nil
}
