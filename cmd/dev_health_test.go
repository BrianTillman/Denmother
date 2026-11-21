package cmd

import (
	"context"
	"fmt"
	"github.com/BrianTillman/Denmother/internal/operator"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
)

func TestDevURLReachableRequiresHTTP200OnManifest(t *testing.T) {
	for _, status := range []int{200, 301, 401, 500, 503} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			var apiProbes int
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/manifest.json" {
					apiProbes++
				}
				w.WriteHeader(status)
			}))
			defer server.Close()
			if got := devURLReachable(server.URL); got != (status == http.StatusOK) {
				t.Fatalf("HTTP %d reachable=%v", status, got)
			}
			if apiProbes != 0 {
				t.Fatalf("reachability made %d unauthenticated API probes", apiProbes)
			}
		})
	}
}

func TestDevImageDriftChecksReferencesAndImageIDs(t *testing.T) {
	for _, tc := range []struct {
		name, desired, running, desiredID, runningID string
		drift                                        bool
	}{
		{"matching", "image:v1", "image:v1", "sha256:one", "sha256:one", false},
		{"different version", "image:v2", "image:v1", "sha256:two", "sha256:one", true},
		{"moved tag", "image:stable", "image:stable", "sha256:two", "sha256:one", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			env := &devEnvironment{ProjectName: "isolated-example", ComposeFile: "/example/compose.yml"}
			run := func(_ context.Context, args ...string) ([]byte, error) {
				switch args[0] {
				case "compose":
					return []byte(fmt.Sprintf(`{"services":{"homeassistant":{"image":%q}}}`, tc.desired)), nil
				case "inspect":
					if args[len(args)-1] != "isolated-example-homeassistant" {
						t.Fatal("inspected another runtime")
					}
					return []byte(fmt.Sprintf("%q %q", tc.running, tc.runningID)), nil
				case "image":
					return []byte(tc.desiredID), nil
				}
				return nil, fmt.Errorf("unexpected Docker operation")
			}
			got := inspectDevImage(context.Background(), env, run)
			if !got.Verified || got.Drift != tc.drift || got.Desired != tc.desired || got.Running != tc.running || got.DesiredID != tc.desiredID || got.RunningID != tc.runningID {
				t.Fatalf("image identity result: %+v", got)
			}
		})
	}
}

func TestDevImageInspectionFailuresStayUnverifiedAndDoNotExposeOutput(t *testing.T) {
	for _, failure := range []string{"compose", "inspect", "image"} {
		t.Run(failure, func(t *testing.T) {
			run := func(_ context.Context, args ...string) ([]byte, error) {
				if args[0] == failure {
					return []byte("private fixture output"), fmt.Errorf("private fixture error")
				}
				if args[0] == "compose" {
					return []byte(`{"services":{"homeassistant":{"image":"image:v1"}}}`), nil
				}
				return []byte(`"image:v1" "sha256:one"`), nil
			}
			got := inspectDevImage(context.Background(), &devEnvironment{}, run)
			if got.Verified || got.Error == "" || strings.Contains(got.Error, "private fixture") {
				t.Fatalf("unsafe image failure: %+v", got)
			}
		})
	}
}

func TestDevReadinessRequiresAuthenticatedRunningHA(t *testing.T) {
	for _, tc := range []struct {
		name, body, token    string
		status               int
		auth, running, ready bool
	}{
		{"ready", `{"state":"RUNNING","safe_mode":false}`, "fixture-token", 200, true, true, true},
		{"starting", `{"state":"STARTING"}`, "fixture-token", 200, true, false, false},
		{"safe mode", `{"state":"RUNNING","safe_mode":true}`, "fixture-token", 200, true, true, false},
		{"recovery mode", `{"state":"RUNNING","recovery_mode":true}`, "fixture-token", 200, true, true, false},
		{"malformed", `not HA config`, "fixture-token", 200, false, false, false},
		{"missing state", `{}`, "fixture-token", 200, false, false, false},
		{"invalid token", `{}`, "fixture-token", 401, false, false, false},
		{"server error", `{}`, "fixture-token", 500, false, false, false},
		{"redirect", `{}`, "fixture-token", 302, false, false, false},
		{"no token", `{"state":"RUNNING"}`, "", 200, false, false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var calls atomic.Int64
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				if r.URL.Path != "/api/config" || r.Header.Get("Authorization") != "Bearer fixture-token" {
					t.Errorf("unauthenticated/wrong readiness request")
				}
				w.Header().Set("Location", "/redirect-target")
				w.WriteHeader(tc.status)
				fmt.Fprint(w, tc.body)
			}))
			defer server.Close()
			run := func(_ context.Context, args ...string) ([]byte, error) {
				if !reflect.DeepEqual(args, []string{"exec", "selected-homeassistant", "cat", "/run/hass/token.env"}) {
					t.Fatalf("token read outside selected container: %v", args)
				}
				return []byte("HASS_BEARER_TOKEN=" + tc.token + "\n"), nil
			}
			auth, running, ready, reason := inspectDevReadiness(context.Background(), &devEnvironment{ProjectName: "selected", HAURL: server.URL}, run)
			if auth != tc.auth || running != tc.running || ready != tc.ready || (ready && reason != "") || (!ready && reason == "") {
				t.Fatalf("auth=%v running=%v ready=%v reason=%q", auth, running, ready, reason)
			}
			want := int64(1)
			if tc.token == "" {
				want = 0
			}
			if calls.Load() != want {
				t.Fatalf("made %d requests, expected %d; redirects/tokenless probes must not run", calls.Load(), want)
			}
		})
	}
}

func TestDevStatusNeverReportsPartialEvidenceAsHealthy(t *testing.T) {
	good := devStatusSummary{Environment: &devEnvironment{ProjectName: "selected"}, ComposeFileExists: true, DockerAvailable: true, HAReachable: true, HAAuthenticated: true, HARunning: true, HAReady: true, HAImage: devImageStatus{Verified: true}}
	if status, _ := devStatusOutcome(good); status != operator.StatusSuccess {
		t.Fatal(status)
	}
	for _, tc := range []struct {
		name   string
		change func(*devStatusSummary)
		want   operator.Status
	}{
		{"not authenticated", func(s *devStatusSummary) { s.HAAuthenticated = false; s.HAReady = false }, operator.StatusPartial},
		{"not running", func(s *devStatusSummary) { s.HARunning = false; s.HAReady = false }, operator.StatusPartial},
		{"image unverified", func(s *devStatusSummary) { s.HAImage.Verified = false }, operator.StatusPartial},
		{"image drift", func(s *devStatusSummary) { s.HAImage.Drift = true }, operator.StatusFailure},
		{"service missing", func(s *devStatusSummary) { s.MissingServices = []string{"ui-proxy"} }, operator.StatusPartial},
		{"Docker missing", func(s *devStatusSummary) { s.DockerAvailable = false }, operator.StatusWarning},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := good
			tc.change(&s)
			if status, _ := devStatusOutcome(s); status != tc.want {
				t.Fatalf("status=%s want=%s", status, tc.want)
			}
		})
	}
}
