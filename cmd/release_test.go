package cmd

import (
	"archive/tar"
	"compress/gzip"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestReleaseArchiveExcludesRuntimeAndCredentials(t *testing.T) {
	root := t.TempDir()
	files := append(releaseAssetFiles(), "dm", "docs/private-review.md", "examples/quickstart/ha-config/secrets.yaml", "examples/quickstart/ha-config/unreviewed.yaml", "examples/quickstart/.devcontainer/worktrees/private/token.env", "examples/quickstart/.env", "examples/quickstart/ha-config/unknown.bin")
	for _, name := range files {
		path := filepath.Join(root, name)
		os.MkdirAll(filepath.Dir(path), 0755)
		os.WriteFile(path, []byte("fixture"), 0600)
	}
	archivePath := filepath.Join(t.TempDir(), "release.tar.gz")
	if err := writeReleaseArchive(archivePath, root, filepath.Join(root, "dm")); err != nil {
		t.Fatal(err)
	}
	file, _ := os.Open(archivePath)
	defer file.Close()
	gz, err := gzip.NewReader(file)
	if err != nil {
		t.Fatal(err)
	}
	defer gz.Close()
	archive := tar.NewReader(gz)
	foundExample := false
	for {
		header, err := archive.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(header.Name, ".env") || strings.Contains(header.Name, "worktrees") || strings.HasSuffix(header.Name, ".bin") || strings.Contains(header.Name, "secrets") || strings.Contains(header.Name, "private-review") || strings.Contains(header.Name, "unreviewed") {
			t.Fatalf("private file shipped: %s", header.Name)
		}
		if header.Name == "examples/quickstart/ha-config/configuration.yaml" {
			foundExample = true
		}
	}
	if !foundExample {
		t.Fatal("missing runnable example")
	}
}

func TestReleaseAssetsRejectSymlinks(t *testing.T) {
	for _, name := range []string{"LICENSE", "docs", "examples/quickstart"} {
		t.Run(name, func(t *testing.T) {
			root := t.TempDir()
			for _, asset := range releaseAssetFiles() {
				path := filepath.Join(root, asset)
				if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(path, []byte("fixture"), 0600); err != nil {
					t.Fatal(err)
				}
			}
			path := filepath.Join(root, name)
			moved := filepath.Join(t.TempDir(), "private")
			if err := os.Rename(path, moved); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(moved, path); err != nil {
				t.Skipf("symlinks unavailable: %v", err)
			}
			if err := checkReleaseSource(root); err == nil {
				t.Fatal("source check accepted symlink")
			}
			binary := filepath.Join(root, "dm")
			if err := os.WriteFile(binary, []byte("binary"), 0700); err != nil {
				t.Fatal(err)
			}
			if err := writeReleaseArchive(filepath.Join(t.TempDir(), "release.tar.gz"), root, binary); err == nil {
				t.Fatal("archive accepted symlink")
			}
		})
	}
}

func TestReleaseRootUsesPublicModulePath(t *testing.T) {
	previous := releaseSource
	t.Cleanup(func() { releaseSource = previous })
	releaseSource = t.TempDir()
	for _, tc := range []struct {
		contents string
		valid    bool
	}{
		{"module github.com/BrianTillman/Denmother\n\ngo 1.26.8\n", true},
		{"module example.com/unrelated\n// /denmother\n", false},
	} {
		if err := os.WriteFile(filepath.Join(releaseSource, "go.mod"), []byte(tc.contents), 0600); err != nil {
			t.Fatal(err)
		}
		_, err := releaseRoot()
		if (err == nil) != tc.valid {
			t.Fatalf("releaseRoot error = %v; valid = %v", err, tc.valid)
		}
	}
}
