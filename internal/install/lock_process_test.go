package install

import (
	"bufio"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

func TestInstallerLockProcessHelper(t *testing.T) {
	destination := os.Getenv("DM_INSTALL_LOCK_FIXTURE")
	if destination == "" {
		return
	}
	release, err := lock(destination)
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	fmt.Println("locked")
	time.Sleep(time.Minute)
}
func TestInstallerLockReleasedAfterProcessDeath(t *testing.T) {
	destination := filepath.Join(t.TempDir(), "skill")
	command := exec.Command(os.Args[0], "-test.run=^TestInstallerLockProcessHelper$")
	command.Env = append(os.Environ(), "DM_INSTALL_LOCK_FIXTURE="+destination)
	output, err := command.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err = command.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = command.Process.Kill(); _ = command.Wait() })
	ready := make(chan bool, 1)
	go func() { scanner := bufio.NewScanner(output); ready <- scanner.Scan() && scanner.Text() == "locked" }()
	select {
	case ok := <-ready:
		if !ok {
			t.Fatal("child did not acquire lock")
		}
	case <-time.After(10 * time.Second):
		t.Fatal("child lock timed out")
	}
	if release, err := lock(destination); err == nil {
		release()
		t.Fatal("another process acquired held lock")
	}
	if err = command.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	_ = command.Wait()
	release, err := lock(destination)
	if err != nil {
		t.Fatalf("dead process retained lock: %v", err)
	}
	release()
}
