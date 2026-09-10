package cmd

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/BrianTillman/Denmother/internal/validator"
)

func TestProjectBlockingGitHelper(t *testing.T) {
	if os.Getenv("DM_PROJECT_BLOCK_HELPER") != "1" {
		return
	}
	if err := os.WriteFile(os.Getenv("DM_PROJECT_BLOCK_READY"), []byte("ready"), 0600); err != nil {
		os.Exit(2)
	}
	time.Sleep(time.Minute)
	os.Exit(0)
}

func TestProjectGitReviewCancelsBeforeFallback(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("uses a POSIX executable shim")
	}
	root := t.TempDir()
	selectAssistanceProject(t, root)
	marker := filepath.Join(root, "ready")
	t.Setenv("DM_PROJECT_BLOCK_HELPER", "1")
	t.Setenv("DM_PROJECT_BLOCK_READY", marker)
	t.Setenv("DM_PROJECT_TEST_BINARY", os.Args[0])
	t.Setenv("PATH", root+string(os.PathListSeparator)+os.Getenv("PATH"))
	if err := os.WriteFile(filepath.Join(root, "git"), []byte("#!/bin/sh\nexec \"$DM_PROJECT_TEST_BINARY\" -test.run=^TestProjectBlockingGitHelper$\n"), 0700); err != nil {
		t.Fatal(err)
	}
	previous := rootCmd.Context()
	t.Cleanup(func() { rootCmd.SetContext(previous) })
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	rootCmd.SetContext(ctx)
	done := make(chan error, 1)
	go func() { _, err := changedFilesForReview(); done <- err }()
	deadline := time.Now().Add(3 * time.Second)
	for {
		if _, err := os.Stat(marker); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("project Git subprocess did not start")
		}
		time.Sleep(5 * time.Millisecond)
	}
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("canceled Git review did not preserve cancellation: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("project Git review ignored cancellation")
	}
}

func TestValidationSuitePreservesCanceledCommandContext(t *testing.T) {
	previous := rootCmd.Context()
	t.Cleanup(func() { rootCmd.SetContext(previous) })
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	rootCmd.SetContext(ctx)
	summary := runValidationSuite(t.TempDir(), true)
	if len(summary.IncompleteChecks) != 5 || len(summary.FailedChecks) != 0 {
		t.Fatalf("cancellation was mistaken for completed validation: %+v", summary)
	}
	for _, result := range summary.Results {
		if result.Status != validator.CheckIncomplete || !errors.Is(result.Err(), context.Canceled) {
			t.Errorf("cancellation evidence lost: %+v", result)
		}
	}
}
