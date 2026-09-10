package mcpserver

import (
	"fmt"
	"os"
	"os/exec"

	"golang.org/x/sys/windows"
)

func tryFileLock(file *os.File) (bool, error) {
	err := windows.LockFileEx(windows.Handle(file.Fd()), windows.LOCKFILE_EXCLUSIVE_LOCK|windows.LOCKFILE_FAIL_IMMEDIATELY, 0, 1, 0, &windows.Overlapped{})
	if err == windows.ERROR_LOCK_VIOLATION {
		return false, nil
	}
	return err == nil, err
}
func configureProcess(cmd *exec.Cmd) {}

func killProcessTree(cmd *exec.Cmd) {
	if cmd.Process != nil {
		_ = cmd.Process.Kill()
	}
}
func cleanupProcessTree(cmd *exec.Cmd) {}

func openEvidence(root *os.Root, path string) (*os.File, error) { return root.Open(path) }

func openRuntimeLock(root *os.Root, path string) (*os.File, error) {
	if info, err := root.Lstat(path); err == nil && info.Mode()&os.ModeSymlink != 0 {
		return nil, fmt.Errorf("runtime lock must not be a symlink")
	}
	return root.OpenFile(path, os.O_CREATE|os.O_RDWR, 0600)
}

// Windows access is controlled by the user's temporary directory ACL.
func privateLockInfo(info os.FileInfo) bool { return true }
