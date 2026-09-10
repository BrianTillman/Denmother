package cmd

import (
	"context"
	"fmt"
	"os/exec"
	"runtime"
	"strings"
	"time"
)

func dashboardBrowserToolchain() error {
	for _, name := range []string{"node", "npm"} {
		if _, err := exec.LookPath(name); err != nil {
			return fmt.Errorf("dashboard rendering requires Node.js and npm on PATH (%s is missing); install a supported Node.js LTS release", name)
		}
	}
	return nil
}

// Exercise the same headless executable Playwright will launch. Checking only
// Chromium's download path misses Linux shared-library and sandbox failures.
func preflightDashboardBrowser(ctx context.Context, runnerDir string) error {
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, "node", "-e", `const {chromium}=require('playwright'); chromium.launch({headless:true}).then(browser=>browser.close()).catch(error=>{process.stderr.write(String(error));process.exitCode=1;});`)
	command.Dir = runnerDir
	output, err := command.CombinedOutput()
	if err == nil {
		return nil
	}
	if ctx.Err() != nil {
		return fmt.Errorf("chromium preflight: %w", ctx.Err())
	}
	return dashboardBrowserDependencyError(runtime.GOOS, runnerDir, strings.TrimSpace(string(output)))
}

func dashboardBrowserDependencyError(goos, runnerDir, diagnostic string) error {
	setup := fmt.Sprintf("from %s run `npx playwright install chromium`", runnerDir)
	if goos == "linux" {
		setup = fmt.Sprintf("from %s run `npx playwright install-deps chromium` to install Linux browser libraries, then retry (administrator access may be required)", runnerDir)
	}
	return fmt.Errorf("chromium cannot launch; %s. Browser diagnostic: %s", setup, redactDashboardDiagnostic(diagnostic))
}
