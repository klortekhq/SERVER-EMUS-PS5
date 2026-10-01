package catalog

import (
	"path/filepath"
	"strings"
)

// resolveExistingWithinRoot resolves symlinks for an existing candidate and
// proves that the resolved path remains inside resolvedRoot. resolvedRoot must
// itself already be an EvalSymlinks result.
func resolveExistingWithinRoot(resolvedRoot, candidate string) (string, bool) {
	resolved, err := filepath.EvalSymlinks(candidate)
	if err != nil {
		return "", false
	}
	resolved = filepath.Clean(resolved)

	relative, err := filepath.Rel(resolvedRoot, resolved)
	if err != nil || filepath.IsAbs(relative) ||
		relative == ".." ||
		strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return "", false
	}
	return resolved, true
}
