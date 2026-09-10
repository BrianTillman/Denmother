package util

import (
	"os"
	"path/filepath"
	"testing"
)

func TestAtomicSnapshotReplacesCompleteFileAndCleansFailedRename(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "snapshot")
	if err := os.WriteFile(path, []byte("old"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := WriteFileAtomic(path, []byte("new complete snapshot"), 0644); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(path)
	if err != nil || string(got) != "new complete snapshot" {
		t.Fatalf("snapshot=%s err=%v", got, err)
	}
	blocked := filepath.Join(root, "directory")
	if err := os.Mkdir(blocked, 0755); err != nil {
		t.Fatal(err)
	}
	if err := WriteFileAtomic(blocked, []byte("cannot replace a directory"), 0644); err == nil {
		t.Fatal("expected rename failure")
	}
	leftovers, err := filepath.Glob(filepath.Join(root, ".dm-snapshot-*"))
	if err != nil || len(leftovers) != 0 {
		t.Fatalf("temporary files leaked: %v %v", leftovers, err)
	}
}
