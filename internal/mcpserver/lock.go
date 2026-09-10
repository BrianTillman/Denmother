package mcpserver

import (
	"context"
	"crypto/sha256"
	"fmt"
	"os"
	"os/user"
	"path/filepath"
	"time"
)

func acquireRuntimeLock(ctx context.Context, key string) (func(), error) {
	current, err := user.Current()
	if err != nil {
		return nil, err
	}
	dir := filepath.Join(os.TempDir(), fmt.Sprintf("denmother-mcp-locks-%x", sha256.Sum256([]byte(current.Uid))))
	if err := os.Mkdir(dir, 0700); err != nil && !os.IsExist(err) {
		return nil, err
	}
	info, err := os.Lstat(dir)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 || !privateLockInfo(info) {
		return nil, fmt.Errorf("unsafe runtime lock directory")
	}
	root, err := os.OpenRoot(dir)
	if err != nil {
		return nil, err
	}
	defer root.Close()
	file, err := openRuntimeLock(root, fmt.Sprintf("%x.lock", sha256.Sum256([]byte(key))))
	if err != nil {
		return nil, err
	}
	fileInfo, err := file.Stat()
	if err != nil || !fileInfo.Mode().IsRegular() || !privateLockInfo(fileInfo) {
		file.Close()
		return nil, fmt.Errorf("unsafe runtime lock file")
	}
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	for {
		if err := ctx.Err(); err != nil {
			file.Close()
			return nil, err
		}
		locked, err := tryFileLock(file)
		if err != nil {
			file.Close()
			return nil, err
		}
		if locked {
			return func() { _ = file.Close() }, nil
		} // Closing releases OS locks even after a crash.
		select {
		case <-ctx.Done():
			file.Close()
			return nil, ctx.Err()
		case <-ticker.C:
		}
	}
}
