package operator

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/BrianTillman/Denmother/internal/haconfig"
)

func TestResolveTargetContextHonorsCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	target, err := ResolveTargetContext(ctx, haconfig.InstanceProd, haconfig.InstanceFlags{ProdURL: "https://ha.example.test", ProdToken: "synthetic"}, ModeReadOnly, false)
	if target != nil || !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled target resolution succeeded: %+v %v", target, err)
	}
}

func TestMetadataBlockingGitHelper(t *testing.T) {
	if os.Getenv("DM_OPERATOR_BLOCK_HELPER") != "1" {
		return
	}
	if err := os.WriteFile(os.Getenv("DM_OPERATOR_BLOCK_READY"), []byte("ready"), 0600); err != nil {
		os.Exit(2)
	}
	time.Sleep(time.Minute)
	os.Exit(0)
}

func TestRuntimeMetadataCancelsBlockingGit(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("uses a POSIX executable shim")
	}
	dir := t.TempDir()
	marker := filepath.Join(dir, "ready")
	t.Setenv("DM_OPERATOR_BLOCK_HELPER", "1")
	t.Setenv("DM_OPERATOR_BLOCK_READY", marker)
	t.Setenv("DM_OPERATOR_TEST_BINARY", os.Args[0])
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	if err := os.WriteFile(filepath.Join(dir, "git"), []byte("#!/bin/sh\nexec \"$DM_OPERATOR_TEST_BINARY\" -test.run=^TestMetadataBlockingGitHelper$\n"), 0700); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan *Runtime, 1)
	go func() { done <- NewRuntimeContext(ctx, "test", true, io.Discard) }()
	deadline := time.Now().Add(3 * time.Second)
	for {
		if _, err := os.Stat(marker); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("Git metadata subprocess did not start")
		}
		time.Sleep(5 * time.Millisecond)
	}
	cancel()
	select {
	case rt := <-done:
		if rt.result.GitHead != "" || rt.result.GitDirty {
			t.Fatalf("canceled metadata was treated as available: %+v", rt.result)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("runtime metadata ignored cancellation")
	}
}
