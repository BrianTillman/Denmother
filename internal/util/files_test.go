package util

import (
	"os"
	"path/filepath"
	"testing"
)

func TestIsYAMLFile(t *testing.T) {
	tests := []struct {
		path   string
		expect bool
	}{
		{"config.yaml", true},
		{"config.yml", true},
		{"CONFIG.YAML", true},
		{"config.YML", true},
		{"readme.md", false},
		{"script.py", false},
		{"config.json", false},
		{"", false},
		{"file", false},
		{".yaml", true},
		{"path/to/file.yaml", true},
		{"path/to/file.txt", false},
	}

	for _, tt := range tests {
		t.Run(tt.path, func(t *testing.T) {
			got := IsYAMLFile(tt.path)
			if got != tt.expect {
				t.Errorf("IsYAMLFile(%q) = %v, want %v", tt.path, got, tt.expect)
			}
		})
	}
}

func TestFileExists(t *testing.T) {
	tmpDir := t.TempDir()

	testFile := filepath.Join(tmpDir, "test.yaml")
	os.WriteFile(testFile, []byte("key: value\n"), 0644)

	if !FileExists(testFile) {
		t.Errorf("FileExists(%q) = false, want true", testFile)
	}

	nonExistent := filepath.Join(tmpDir, "nonexistent.yaml")
	if FileExists(nonExistent) {
		t.Errorf("FileExists(%q) = true, want false", nonExistent)
	}

	// FileExists also accepts directories.
	if !FileExists(tmpDir) {
		t.Errorf("FileExists(%q) = false, want true (directories exist too)", tmpDir)
	}
}

func TestDirExists(t *testing.T) {
	tmpDir := t.TempDir()

	subDir := filepath.Join(tmpDir, "subdir")
	os.MkdirAll(subDir, 0755)

	testFile := filepath.Join(tmpDir, "test.yaml")
	os.WriteFile(testFile, []byte("key: value\n"), 0644)

	if !DirExists(subDir) {
		t.Errorf("DirExists(%q) = false, want true", subDir)
	}

	nonExistent := filepath.Join(tmpDir, "nonexistent")
	if DirExists(nonExistent) {
		t.Errorf("DirExists(%q) = true, want false", nonExistent)
	}

	if DirExists(testFile) {
		t.Errorf("DirExists(%q) = true, want false (it's a file)", testFile)
	}
}

func TestReadFile(t *testing.T) {
	tmpDir := t.TempDir()

	testFile := filepath.Join(tmpDir, "test.yaml")
	expectedContent := "key: value\nlist:\n  - item1\n  - item2\n"
	os.WriteFile(testFile, []byte(expectedContent), 0644)

	content, err := ReadFile(testFile)
	if err != nil {
		t.Errorf("ReadFile(%q) error = %v", testFile, err)
	}
	if content != expectedContent {
		t.Errorf("ReadFile(%q) = %q, want %q", testFile, content, expectedContent)
	}

	_, err = ReadFile(filepath.Join(tmpDir, "nonexistent.yaml"))
	if err == nil {
		t.Error("ReadFile() should error for non-existing file")
	}
}

func TestRelPath(t *testing.T) {
	tests := []struct {
		base     string
		path     string
		expected string
	}{
		{"/home/user/config", "/home/user/config/automations/test.yaml", "automations/test.yaml"},
		{"/home/user/config", "/home/user/config/test.yaml", "test.yaml"},
		{"/home/user", "/home/user/config/test.yaml", "config/test.yaml"},
	}

	for _, tt := range tests {
		t.Run(tt.path, func(t *testing.T) {
			got := RelPath(tt.base, tt.path)
			if got != tt.expected {
				t.Errorf("RelPath(%q, %q) = %q, want %q", tt.base, tt.path, got, tt.expected)
			}
		})
	}
}

func TestFileWalker_GlobFiles(t *testing.T) {
	tmpDir := t.TempDir()

	os.WriteFile(filepath.Join(tmpDir, "config.yaml"), []byte("key: value\n"), 0644)
	os.WriteFile(filepath.Join(tmpDir, "test.yaml"), []byte("key: value\n"), 0644)
	os.WriteFile(filepath.Join(tmpDir, "readme.md"), []byte("# Readme\n"), 0644)

	os.MkdirAll(filepath.Join(tmpDir, "automations"), 0755)
	os.WriteFile(filepath.Join(tmpDir, "automations", "test.yaml"), []byte("alias: Test\n"), 0644)

	fw := NewFileWalker(tmpDir)

	files, err := fw.GlobFiles("*.yaml")
	if err != nil {
		t.Errorf("GlobFiles(*.yaml) error = %v", err)
	}
	if len(files) != 2 {
		t.Errorf("GlobFiles(*.yaml) returned %d files, want 2", len(files))
	}

	files, err = fw.GlobFiles("automations/*.yaml")
	if err != nil {
		t.Errorf("GlobFiles(automations/*.yaml) error = %v", err)
	}
	if len(files) != 1 {
		t.Errorf("GlobFiles(automations/*.yaml) returned %d files, want 1", len(files))
	}

	files, err = fw.GlobFiles("**/*.yaml")
	if err != nil {
		t.Errorf("GlobFiles(**/*.yaml) error = %v", err)
	}
	if len(files) < 1 {
		t.Errorf("GlobFiles(**/*.yaml) returned %d files, want >= 1", len(files))
	}
}

func TestFileWalker_WalkYAMLFiles(t *testing.T) {
	tmpDir := t.TempDir()

	os.WriteFile(filepath.Join(tmpDir, "config.yaml"), []byte("key: value\n"), 0644)
	os.WriteFile(filepath.Join(tmpDir, "secrets.yaml"), []byte("password: secret\n"), 0644)

	os.MkdirAll(filepath.Join(tmpDir, "automations"), 0755)
	os.WriteFile(filepath.Join(tmpDir, "automations", "test.yaml"), []byte("alias: Test\n"), 0644)
	os.WriteFile(filepath.Join(tmpDir, "automations", "secrets.yaml"), []byte("api_key: secret\n"), 0644)

	fw := NewFileWalker(tmpDir)

	files, err := fw.WalkYAMLFiles("automations", false)
	if err != nil {
		t.Errorf("WalkYAMLFiles() error = %v", err)
	}
	if len(files) != 2 {
		t.Errorf("WalkYAMLFiles(excludeSecrets=false) returned %d files, want 2", len(files))
	}

	files, err = fw.WalkYAMLFiles("automations", true)
	if err != nil {
		t.Errorf("WalkYAMLFiles() error = %v", err)
	}
	if len(files) != 1 {
		t.Errorf("WalkYAMLFiles(excludeSecrets=true) returned %d files, want 1", len(files))
	}
}

func TestNewFileWalker(t *testing.T) {
	fw := NewFileWalker("/test/path")
	if fw == nil {
		t.Error("NewFileWalker() returned nil")
	}
	if fw.basePath != "/test/path" {
		t.Errorf("FileWalker.basePath = %q, want %q", fw.basePath, "/test/path")
	}
}
