package util

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/bmatcuk/doublestar/v4"
)

// FileWalker handles file discovery with glob patterns
type FileWalker struct {
	basePath string
}

// NewFileWalker creates a new FileWalker
func NewFileWalker(basePath string) *FileWalker {
	return &FileWalker{basePath: basePath}
}

// GlobFiles returns files matching the glob pattern relative to base path
func (fw *FileWalker) GlobFiles(pattern string) ([]string, error) {
	fullPattern := filepath.Join(fw.basePath, pattern)
	return doublestar.FilepathGlob(fullPattern)
}

// WalkYAMLFiles walks all YAML files in a directory recursively
func (fw *FileWalker) WalkYAMLFiles(dir string, excludeSecrets bool) ([]string, error) {
	var files []string
	fullDir := filepath.Join(fw.basePath, dir)

	err := filepath.Walk(fullDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil
		}
		if info.IsDir() {
			return nil
		}
		if !IsYAMLFile(path) {
			return nil
		}
		if excludeSecrets && strings.Contains(filepath.Base(path), "secrets") {
			return nil
		}
		files = append(files, path)
		return nil
	})

	return files, err
}

// IsYAMLFile checks if a file has a YAML extension
func IsYAMLFile(path string) bool {
	ext := strings.ToLower(filepath.Ext(path))
	return ext == ".yaml" || ext == ".yml"
}

// FileExists checks if a file exists
func FileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

// DirExists checks if a directory exists
func DirExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.IsDir()
}

// ReadFile reads a file's contents
func ReadFile(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	return string(data), nil
}

// RelPath returns path relative to base, or original if not possible
func RelPath(base, path string) string {
	rel, err := filepath.Rel(base, path)
	if err != nil {
		return path
	}
	return rel
}
