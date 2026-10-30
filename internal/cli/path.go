package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// displayPath folds only complete home-directory segments for terminal output.
func displayPath(path string) string {
	home, err := os.UserHomeDir()
	if err != nil || !filepath.IsAbs(path) {
		return path
	}
	rel, err := filepath.Rel(home, path)
	if err != nil || !filepath.IsLocal(rel) {
		return path
	}
	return filepath.Join("~", rel)
}

func expandPath(path string) (string, error) {
	if path != "~" && !strings.HasPrefix(path, "~/") && !strings.HasPrefix(path, "~"+string(filepath.Separator)) {
		return path, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("expanding home directory: %w", err)
	}
	return filepath.Join(home, strings.TrimPrefix(path[1:], string(filepath.Separator))), nil
}
