//go:build windows

package catalog

import "os"

func openCandidate(path string) (*os.File, error) {
	return os.Open(path)
}
