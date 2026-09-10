package cmd

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// Run with DENMOTHER_TEST_PLAYWRIGHT_DIR pointing at a prepared runner directory.
// The fixture server is synthetic; this test never contacts a Home Assistant.
func TestDashboardBrowserAcceptance(t *testing.T) {
	runner := os.Getenv("DENMOTHER_TEST_PLAYWRIGHT_DIR")
	if runner == "" {
		t.Skip("set DENMOTHER_TEST_PLAYWRIGHT_DIR to exercise Chromium")
	}
	runner, err := filepath.Abs(runner)
	if err != nil {
		t.Fatal(err)
	}
	var requestsMu sync.Mutex
	requests := make(map[string]int)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestsMu.Lock()
		requests[r.URL.Path]++
		requestsMu.Unlock()
		w.Header().Set("Content-Type", "text/html")
		if strings.HasPrefix(r.URL.Path, "/login-dashboard/") {
			if _, err := r.Cookie("authenticated"); err != nil {
				http.Redirect(w, r, "/auth/login?next="+r.URL.Path, http.StatusFound)
				return
			}
		}
		switch r.URL.Path {
		case "/incomplete-onboarding/0":
			http.Redirect(w, r, "/auth/login", http.StatusFound)
			return
		case "/auth/login":
			next := r.URL.Query().Get("next")
			if next == "" {
				next = "/onboarding.html"
			}
			_, _ = fmt.Fprintf(w, `<form onsubmit="event.preventDefault();document.cookie='authenticated=yes;path=/';location.href='%s'"><input name="username"><input name="password" type="password"><button>Log in</button></form>`, next)
			return
		case "/onboarding.html":
			_, _ = w.Write([]byte("<p>Finish onboarding</p>"))
			return
		case "/missing-resource":
			w.WriteHeader(http.StatusNotFound)
			return
		}
		content := "<ha-card style='display:block;width:200px;height:100px'>Healthy state</ha-card>"
		switch {
		case strings.HasSuffix(r.URL.Path, "/failed-resource"):
			content += "<img src='/missing-resource?token=synthetic-secret' />"
		case strings.HasSuffix(r.URL.Path, "/page-error"):
			content += "<img src='data:,' onerror='throw new Error(\"dashboard startup failed\")' />"
		case strings.HasSuffix(r.URL.Path, "/rejected-object"):
			content += "<img src='data:,' onerror='Promise.reject({code:\"fixture_failure\",message:\"request failed\",token:\"synthetic-secret\"})' />"
		case strings.HasSuffix(r.URL.Path, "/empty"):
			content = "<p>Lots of ordinary dashboard text, but no card.</p>"
		case strings.HasSuffix(r.URL.Path, "/nested"):
			content = "<nested-card style='display:block;width:250px;height:150px'></nested-card>"
		case strings.HasSuffix(r.URL.Path, "/delayed"):
			content = "<delayed-card style='display:block;width:250px;height:150px'>Loading custom canvas</delayed-card>"
		case strings.HasSuffix(r.URL.Path, "/custom"):
			content = "<synthetic-card style='display:block;width:200px;height:100px'><div>Custom-only content</div></synthetic-card><ha-alert alert-type='info' style='display:block;width:200px;height:30px'>Informational message</ha-alert>"
		case strings.HasSuffix(r.URL.Path, "/broken"):
			content = "<hui-error-card style='display:block;width:200px;height:100px'>Custom element doesn't exist: missing-card</hui-error-card>"
		}
		encoded, _ := json.Marshal(content)
		_, _ = fmt.Fprintf(w, `<!doctype html><html><body><home-assistant-main><hui-root></hui-root></home-assistant-main><script>
  customElements.define('synthetic-card',class extends HTMLElement {});
  customElements.define('nested-card',class extends HTMLElement {connectedCallback(){this.attachShadow({mode:'open'}).innerHTML='<section><canvas width="200" height="100"></canvas></section>';}});
  setTimeout(()=>customElements.define('delayed-card',class extends HTMLElement {connectedCallback(){this.attachShadow({mode:'closed'}).innerHTML='<canvas width="200" height="100"></canvas>';}}),1200);
  document.querySelector('hui-root').attachShadow({mode:'open'}).innerHTML=%s;
  </script></body></html>`, encoded)
	}))
	defer server.Close()
	for _, tc := range []struct {
		name       string
		views      []string
		custom     []string
		fail       bool
		onboarding bool
		login      bool
		pageError  string
	}{
		{name: "all views", views: []string{"first", "second"}},
		{name: "login preserves first requested view", views: []string{"first", "second"}, login: true},
		{name: "first view page errors retained", views: []string{"page-error"}, fail: true, pageError: "dashboard startup failed"},
		{name: "rejected object retains error code without credentials", views: []string{"rejected-object"}, fail: true, pageError: `"code":"fixture_failure"`},
		{name: "nested shadow canvas card", views: []string{"nested"}, custom: []string{"nested-card"}},
		{name: "delayed registration closed shadow card", views: []string{"delayed"}, custom: []string{"delayed-card"}},
		{name: "incomplete onboarding", onboarding: true, fail: true},
		{name: "failed resource token redaction", views: []string{"failed-resource"}, fail: true},
		{name: "custom-only and informational alert", views: []string{"custom"}, custom: []string{"synthetic-card"}},
		{name: "second view error", views: []string{"first", "broken"}, fail: true},
		{name: "empty view", views: []string{"empty"}, fail: true},
		{name: "missing custom registration", views: []string{"first"}, custom: []string{"missing-card"}, fail: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			requestsMu.Lock()
			clear(requests)
			requestsMu.Unlock()
			dir := t.TempDir()
			script := filepath.Join(dir, "render.cjs")
			if err := os.WriteFile(script, []byte(dashboardRenderScript()), 0600); err != nil {
				t.Fatal(err)
			}
			views, _ := json.Marshal(tc.views)
			custom, _ := json.Marshal(tc.custom)
			if tc.custom == nil {
				custom = []byte("[]")
			}
			ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
			defer cancel()
			command := exec.CommandContext(ctx, "node", script)
			resultFile := filepath.Join(dir, "result.json")
			dashboardURL := server.URL + "/test-dashboard/0"
			if tc.login {
				dashboardURL = server.URL + "/login-dashboard/0"
			}
			if tc.onboarding {
				dashboardURL = server.URL + "/incomplete-onboarding/0"
			}
			command.Env = append(os.Environ(), "NODE_PATH="+filepath.Join(runner, "node_modules"), "DASHBOARD_URL="+dashboardURL, "HA_USERNAME=synthetic", "HA_PASSWORD=synthetic", "SCREENSHOT_PATH="+filepath.Join(dir, "view.png"), "RESULT_PATH="+resultFile, "VIEW_PATHS="+string(views), "CUSTOM_CARDS="+string(custom), "RENDER_TIMEOUT_MS=2000")
			output, err := command.CombinedOutput()
			if err != nil && !tc.onboarding {
				t.Fatalf("browser process: %v %s", err, output)
			}
			var result dashboardRenderCheck
			if err := loadDashboardRenderResult(resultFile, &result); err != nil {
				t.Fatal(err)
			}
			encodedResult, _ := json.Marshal(result)
			if strings.Contains(string(encodedResult), "synthetic-secret") {
				t.Fatal("resource token leaked into browser result")
			}
			if tc.onboarding {
				if !strings.Contains(strings.Join(result.PageErrors, " "), "onboarding is incomplete") {
					t.Fatalf("missing onboarding diagnosis: %+v", result)
				}
				if _, err := os.Stat(result.Screenshot); err != nil {
					t.Fatalf("startup screenshot: %v", err)
				}
				return
			}
			if len(result.Views) != len(tc.views) {
				t.Fatalf("rendered %d views, want %d: %+v", len(result.Views), len(tc.views), result)
			}
			problems := len(result.PageErrors) + len(result.ConsoleErrors) + len(result.VisibleErrors) + len(result.RequestFailures)
			if (problems > 0) != tc.fail {
				t.Fatalf("browser issues = %d, expected failure %v: %+v", problems, tc.fail, result)
			}
			if tc.pageError != "" && !strings.Contains(strings.Join(result.PageErrors, " "), tc.pageError) {
				t.Fatalf("missing page error %q: %+v", tc.pageError, result)
			}
			for i, view := range result.Views {
				if !strings.HasSuffix(view.URL, "/"+tc.views[i]) {
					t.Fatalf("wrong view URL: %s", view.URL)
				}
				if _, err := os.Stat(view.Screenshot); err != nil {
					t.Fatalf("view screenshot: %v", err)
				}
				path := "/test-dashboard/" + tc.views[i]
				wantRequests := 1
				if tc.login {
					path = "/login-dashboard/" + tc.views[i]
					if i == 0 {
						wantRequests++ // Unauthenticated request redirects to login.
					}
				}
				requestsMu.Lock()
				gotRequests := requests[path]
				bootstrapRequests := requests["/test-dashboard/0"] + requests["/login-dashboard/0"]
				requestsMu.Unlock()
				if gotRequests != wantRequests || bootstrapRequests != 0 {
					t.Fatalf("view %s: %d requests (want %d), unrequested bootstrap: %d", path, gotRequests, wantRequests, bootstrapRequests)
				}
			}
			if !tc.fail && result.CardCount < len(tc.views) {
				t.Fatalf("no real cards detected: %+v", result)
			}
		})
	}
}
