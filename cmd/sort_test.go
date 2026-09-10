package cmd

import (
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"testing"
)

func TestStagedSortRespectsSelectedConfiguration(t *testing.T) {
	root := t.TempDir()
	t.Chdir(root)
	previous := sortStaged
	t.Cleanup(func() { sortStaged = previous })
	sortStaged = true
	selected := filepath.Join("selected", "helpers", "room \"name\".yaml")
	for _, name := range []string{selected, "selected-backup/helpers/light.yaml", "ha-config/helpers/light.yaml"} {
		if err := os.MkdirAll(filepath.Dir(name), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(name, []byte("- name: fixture\n"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	for _, args := range [][]string{{"init", "-q"}, {"add", "."}} {
		if output, err := exec.Command("git", args...).CombinedOutput(); err != nil {
			t.Fatalf("prepare staged fixture: %v: %s", err, output)
		}
	}
	for _, base := range []string{"selected", filepath.Join(root, "selected")} {
		files, err := discoverSortFiles(base, nil)
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(files, []string{selected}) {
			t.Fatalf("staged files = %q, want only %q", files, selected)
		}
	}
}
