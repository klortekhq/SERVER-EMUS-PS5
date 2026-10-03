//go:build !aix && !darwin && !dragonfly && !freebsd && !linux && !netbsd && !openbsd && !solaris && !windows

package catalog

import "os"

func openCandidate(path string) (*os.File, error) {
	return os.Open(path)
}
