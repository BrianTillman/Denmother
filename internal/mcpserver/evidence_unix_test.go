//go:build !windows

package mcpserver

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"github.com/BrianTillman/Denmother/internal/operator"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"golang.org/x/sys/unix"
)

func TestEvidenceRejectsFIFOReplacement(t *testing.T) {
	root := fixture(t)
	path := filepath.Join(root, "evidence.json")
	if err := os.WriteFile(path, []byte(`{}`), 0600); err != nil {
		t.Fatal(err)
	}
	a, cs := connect(t, Config{ProjectRoot: root, EvidenceFiles: []string{"evidence.json"}})
	uri := a.evidence.order[0]
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err := unix.Mkfifo(path, 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := cs.ReadResource(context.Background(), &mcp.ReadResourceParams{URI: uri}); err == nil {
		t.Fatal("read a FIFO as evidence")
	}
}

func TestPolicyFIFORejectedWithoutBlocking(t *testing.T) {
	for _, policy := range []string{".denmother.yaml", "ha-config/.denmother.yaml"} {
		for _, explicit := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/explicit=%v", policy, explicit), func(t *testing.T) {
				root := t.TempDir()
				path := filepath.Join(root, policy)
				if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
					t.Fatal(err)
				}
				if err := unix.Mkfifo(path, 0600); err != nil {
					t.Fatal(err)
				}
				ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
				defer cancel()
				args := []string{"mcp", "--project-root", root}
				if explicit {
					args = append(args, "--config=ha-config")
				}
				command := exec.CommandContext(ctx, testBinary, args...)
				out, err := command.CombinedOutput()
				if ctx.Err() != nil {
					t.Fatal("startup blocked on policy FIFO")
				}
				if err == nil {
					t.Fatalf("accepted policy FIFO: %s", out)
				}
			})
		}
	}
}

func TestWorkerExitAndMissingCleanupEvidence(t *testing.T) {
	root := fixture(t)
	a, _ := connect(t, Config{ProjectRoot: root})
	worker := filepath.Join(root, "fixture-worker")
	a.cfg.Executable = worker
	valid, _ := json.Marshal(failure("test", "trigger_failed", "trigger failed"))
	for _, tc := range []struct {
		name, output string
		exit         int
	}{
		{"successful exit contradicts failure", string(valid), 0},
		{"missing envelope", `{"schema_version":"dm.operator.v1","status":"success"}`, 0},
		{"unexpected process exit", string(valid), 7},
		{"overflow", strings.Repeat("x", MaxResultBytes+1), 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			writeProjectInput(t, root, "worker-result.json", tc.output)
			script := fmt.Sprintf("#!/bin/sh\ncat worker-result.json\nexit %d\n", tc.exit)
			if err := os.WriteFile(worker, []byte(script), 0700); err != nil {
				t.Fatal(err)
			}
			result := a.execute(context.Background(), "run_tests", nil)
			if result.Status != operator.StatusFailure || len(result.Steps) != 2 || result.Steps[1].ErrorCode != "cleanup_unverified" {
				t.Fatalf("missing worker failure/cleanup uncertainty: %+v", result)
			}
		})
	}
}

func TestCLIRejectsZeroTimeout(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, testBinary, "mcp", "--project-root", fixture(t), "--timeout=0").CombinedOutput()
	if err == nil || ctx.Err() != nil || !strings.Contains(string(out), "timeout must be") {
		t.Fatalf("zero deadline: %v %s", err, out)
	}
}

func TestRuntimeLockRejectsUnsafeFilesystemEntries(t *testing.T) {
	for _, attack := range []string{"public directory", "symlink", "FIFO", "public file"} {
		t.Run(attack, func(t *testing.T) {
			temp := t.TempDir()
			t.Setenv("TMPDIR", temp)
			current, err := user.Current()
			if err != nil {
				t.Fatal(err)
			}
			dir := filepath.Join(temp, fmt.Sprintf("denmother-mcp-locks-%x", sha256.Sum256([]byte(current.Uid))))
			if err := os.Mkdir(dir, 0700); err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(dir, fmt.Sprintf("%x.lock", sha256.Sum256([]byte("test-target"))))
			switch attack {
			case "public directory":
				if err := os.Chmod(dir, 0777); err != nil {
					t.Fatal(err)
				}
			case "symlink":
				target := filepath.Join(dir, "alternate")
				if err := os.WriteFile(target, nil, 0600); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(target, path); err != nil {
					t.Fatal(err)
				}
			case "FIFO":
				if err := unix.Mkfifo(path, 0600); err != nil {
					t.Fatal(err)
				}
			case "public file":
				if err := os.WriteFile(path, nil, 0600); err != nil {
					t.Fatal(err)
				}
				if err := os.Chmod(path, 0666); err != nil {
					t.Fatal(err)
				}
			}
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			unlock, err := acquireRuntimeLock(ctx, "test-target")
			if unlock != nil {
				unlock()
			}
			if err == nil {
				t.Fatal("acquired unsafe runtime lock")
			}
		})
	}
}
