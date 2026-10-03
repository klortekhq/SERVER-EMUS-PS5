package catalog

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

func pathWithinRoot(resolvedRoot, candidate string) bool {
	root := filepath.Clean(resolvedRoot)
	path := filepath.Clean(candidate)

	relative, err := filepath.Rel(root, path)
	return err == nil &&
		!filepath.IsAbs(relative) &&
		relative != ".." &&
		!strings.HasPrefix(relative, ".."+string(filepath.Separator))
}

// resolveExistingWithinRoot resolves symlinks for an existing candidate and
// proves that the resolved path remains inside resolvedRoot. resolvedRoot must
// itself already be an EvalSymlinks result.
func resolveExistingWithinRoot(resolvedRoot, candidate string) (string, bool) {
	resolved, err := filepath.EvalSymlinks(candidate)
	if err != nil {
		return "", false
	}
	resolved = filepath.Clean(resolved)
	if !pathWithinRoot(resolvedRoot, resolved) {
		return "", false
	}
	return resolved, true
}

// openRegularWithinRoot opens candidate first and then validates the object
// behind the returned handle. This closes the pathname TOCTOU window where a
// previously validated file could be replaced with a symlink outside the
// configured library between resolution and open. Unix opens are non-blocking
// so a concurrent replacement with a FIFO cannot stall the server.
func openRegularWithinRoot(
	resolvedRoot,
	candidate string,
) (*os.File, os.FileInfo, string, error) {
	file, err := openCandidate(candidate)
	if err != nil {
		return nil, nil, "", err
	}

	info, err := file.Stat()
	if err != nil {
		file.Close()
		return nil, nil, "", err
	}
	if !info.Mode().IsRegular() {
		file.Close()
		return nil, nil, "", fmt.Errorf("catalog entry is no longer a regular file")
	}

	openedPath, err := openedFilePath(file, candidate)
	if err != nil {
		file.Close()
		return nil, nil, "", err
	}
	openedPath = filepath.Clean(openedPath)
	if !pathWithinRoot(resolvedRoot, openedPath) {
		file.Close()
		return nil, nil, "", fmt.Errorf("opened file escapes configured library")
	}

	return file, info, openedPath, nil
}
