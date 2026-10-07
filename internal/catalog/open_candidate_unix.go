//go:build aix || darwin || dragonfly || freebsd || linux || netbsd || openbsd || solaris

package catalog

import (
	"os"
	"syscall"
)

func openCandidate(path string) (*os.File, error) {
	return os.OpenFile(path, os.O_RDONLY|syscall.O_NONBLOCK, 0)
}
