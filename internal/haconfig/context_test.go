package haconfig

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestLocalTokenRetryCancellationStopsWaitAndReaders(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	read := make(chan struct{})
	done := make(chan error, 1)
	calls := 0
	go func() {
		_, err := resolveLocalTokenWithDepsContext(ctx, "http://localhost:8123", []tokenReader{func() (string, error) {
			calls++
			close(read)
			return "", errors.New("token not ready")
		}}, func(string, string) bool { t.Error("empty token should not be authenticated"); return false }, 4, time.Hour, nil)
		done <- err
	}()
	<-read
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) || calls != 1 {
			t.Fatalf("retry did not preserve cancellation: calls=%d err=%v", calls, err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("cancellation did not interrupt token retry wait")
	}
}

func TestInstanceResolutionCancelsReadinessRequest(t *testing.T) {
	entered := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		close(entered)
		<-r.Context().Done()
	}))
	defer server.Close()
	t.Setenv("HASS_SERVER", server.URL)
	t.Setenv("HASS_DEV_URL", "")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() {
		_, err := ResolveInstanceConfigContext(ctx, InstanceDev, InstanceFlags{ConfigPath: filepath.Join(t.TempDir(), "config"), DevToken: "synthetic"})
		done <- err
	}()
	<-entered
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("readiness cancellation lost: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("readiness request ignored cancellation")
	}
}

func TestHaconfigBlockingSubprocessHelper(t *testing.T) {
	if os.Getenv("DM_HACONFIG_BLOCK_HELPER") != "1" {
		return
	}
	if err := os.WriteFile(os.Getenv("DM_HACONFIG_BLOCK_READY"), []byte("ready"), 0600); err != nil {
		os.Exit(2)
	}
	time.Sleep(time.Minute)
	os.Exit(0)
}

func TestDiscoverySubprocessCancellationTerminatesProcess(t *testing.T) {
	marker := filepath.Join(t.TempDir(), "ready")
	t.Setenv("DM_HACONFIG_BLOCK_HELPER", "1")
	t.Setenv("DM_HACONFIG_BLOCK_READY", marker)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() {
		_, err := boundedCommandOutput(ctx, os.Args[0], "-test.run=^TestHaconfigBlockingSubprocessHelper$")
		done <- err
	}()
	deadline := time.Now().Add(3 * time.Second)
	for {
		if _, err := os.Stat(marker); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("discovery helper did not start")
		}
		time.Sleep(5 * time.Millisecond)
	}
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("subprocess cancellation lost: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("discovery subprocess did not terminate after cancellation")
	}
}

func TestExplicitTargetHonorsExpiredDeadline(t *testing.T) {
	ctx, cancel := context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
	defer cancel()
	config, err := ResolveInstanceConfigContext(ctx, InstanceProd, InstanceFlags{ProdURL: "https://ha.example.test", ProdToken: "synthetic"})
	if config != nil || !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("expired target resolution succeeded: %+v %v", config, err)
	}
}
