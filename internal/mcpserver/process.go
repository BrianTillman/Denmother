package mcpserver

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/url"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/BrianTillman/Denmother/internal/operator"
	"github.com/google/jsonschema-go/jsonschema"
)

type boundedBuffer struct {
	bytes.Buffer
	limit    int
	overflow bool
}

func (b *boundedBuffer) Write(p []byte) (int, error) {
	n := len(p)
	remaining := b.limit - b.Len()
	if n > remaining {
		b.overflow = true
		p = p[:max(0, remaining)]
	}
	_, _ = b.Buffer.Write(p)
	return n, nil
}

func (a *Adapter) execute(ctx context.Context, name string, args []string) *operator.Result {
	args = append(args, "--config="+a.cfg.ConfigDir, "--json")
	child := exec.CommandContext(ctx, a.cfg.Executable, args...)
	child.Dir = a.cfg.ProjectRoot
	child.Env = a.environment()
	var stdout = boundedBuffer{limit: MaxResultBytes}
	var stderr = boundedBuffer{limit: 64 << 10}
	child.Stdout = &stdout
	child.Stderr = &stderr
	// stdin is a private cancellation channel. Unlike killing the worker,
	// cancellation lets the existing runner perform bounded cleanup on all OSes.
	read, write, err := os.Pipe()
	if err != nil {
		return failure(name, "subprocess_failed", "Cannot create worker cancellation channel.")
	}
	defer read.Close()
	defer write.Close()
	child.Stdin = read
	child.Cancel = func() error { _, err := write.Write([]byte{1}); return err }
	child.WaitDelay = 35 * time.Second
	configureProcess(child)
	if err = child.Start(); err != nil {
		return failure(name, "subprocess_failed", "Could not start the CLI worker.")
	}
	done := make(chan struct{})
	watchDone := make(chan struct{})
	go func() {
		defer close(watchDone)
		select {
		case <-done:
			return
		case <-ctx.Done():
		}
		timer := time.NewTimer(35 * time.Second)
		defer timer.Stop()
		select {
		case <-done:
			return
		case <-timer.C:
			killProcessTree(child)
		}
	}()
	err = child.Wait()
	close(done)
	<-watchDone
	cleanupProcessTree(child)
	if stdout.overflow {
		return workerFailure(name, "output_limit_exceeded", "CLI evidence exceeded the 8 MiB limit; narrow the requested operation.")
	}
	result, decodeErr := decodeWorkerResult(stdout.Bytes())
	if decodeErr != nil {
		code := "subprocess_failed"
		summary := "CLI did not return a valid operator result."
		if ctx.Err() != nil {
			code = "operation_canceled"
			summary = "Operation was canceled before a result was available; cleanup completion is unknown."
		}
		return workerFailure(name, code, summary)
	}
	// Every process exit, including zero, must agree with the domain result.
	if ctx.Err() == nil {
		if (err != nil && child.ProcessState.ExitCode() == 0) || child.ProcessState.ExitCode() != result.ExitCode {
			return workerFailure(name, "subprocess_failed", "CLI process exit did not match its operator result.")
		}
	}
	if ctx.Err() != nil && result.Status != operator.StatusFailure {
		result.Status = operator.StatusFailure
		result.ExitCode = 1
		result.ErrorCode = "operation_canceled"
		result.Steps = append(result.Steps, operator.Step{ID: "mcp-cancellation", Title: "Cancellation", Status: operator.StatusFailure, ErrorCode: "operation_canceled", Summary: "The operation was canceled; completed steps are retained."})
	}
	return result
}

var workerResultSchema = func() *jsonschema.Resolved {
	var schema jsonschema.Schema
	if err := json.Unmarshal([]byte(operator.ResultJSONSchema()), &schema); err != nil {
		panic(err)
	}
	resolved, err := schema.Resolve(nil)
	if err != nil {
		panic(err)
	}
	return resolved
}()

func decodeWorkerResult(data []byte) (*operator.Result, error) {
	// Schema type validation expects JSON numbers as float64. Decode the typed
	// result separately with UseNumber so evidence counters retain precision.
	dec := json.NewDecoder(bytes.NewReader(data))
	var document any
	if err := dec.Decode(&document); err != nil {
		return nil, err
	}
	var trailing any
	if dec.Decode(&trailing) != io.EOF {
		return nil, fmt.Errorf("expected one operator result")
	}
	if err := workerResultSchema.Validate(document); err != nil {
		return nil, err
	}
	var result operator.Result
	dec = json.NewDecoder(bytes.NewReader(data))
	dec.UseNumber()
	if err := dec.Decode(&result); err != nil {
		return nil, err
	}
	if result.ExitCode != operator.ExitCodeForStatus(result.Status) {
		return nil, fmt.Errorf("operator status and exit code disagree")
	}
	return &result, nil
}

func workerFailure(name, code, summary string) *operator.Result {
	failed := failure(name, code, summary)
	if name == "run_tests" {
		failed.Steps = append(failed.Steps, operator.Step{ID: "cleanup", Title: "Cleanup completion", Status: operator.StatusPartial, ErrorCode: "cleanup_unverified", Summary: "The worker did not return valid cleanup evidence."})
	}
	return failed
}

func (a *Adapter) environment() []string {
	// Do not inherit alternate HA targets, tokens, Docker overrides, Git command
	// injection environment, or arbitrary application secrets into the worker.
	var env []string
	for _, key := range []string{"PATH", "HOME", "USERPROFILE", "SYSTEMROOT", "TMPDIR", "TMP", "TEMP", "SSL_CERT_FILE", "SSL_CERT_DIR"} {
		if value, ok := os.LookupEnv(key); ok {
			env = append(env, key+"="+value)
		}
	}
	env = append(env, "DM_MCP_WORKER_ROOT="+a.cfg.ProjectRoot, "GIT_TERMINAL_PROMPT=0", "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL="+os.DevNull, "GIT_CONFIG_COUNT=2", "GIT_CONFIG_KEY_0=core.fsmonitor", "GIT_CONFIG_VALUE_0=false", "GIT_CONFIG_KEY_1=core.hooksPath", "GIT_CONFIG_VALUE_1="+os.DevNull)
	if a.cfg.TargetURL != "" {
		prefix := "HASS_DEV"
		if a.cfg.TargetInstance == "production" {
			prefix = "HASS_PROD"
		}
		env = append(env, prefix+"_URL="+a.cfg.TargetURL, prefix+"_TOKEN="+a.cfg.Token)
	}
	return env
}

// Canonical URL keys serialize servers configured with equivalent spellings.
func runtimeKey(raw string) string {
	u, err := url.Parse(raw)
	if err != nil {
		return raw // Startup validation rejects malformed URLs.
	}
	host := strings.TrimSuffix(strings.ToLower(u.Hostname()), ".")
	port := u.Port()
	if (u.Scheme == "http" && port == "80") || (u.Scheme == "https" && port == "443") {
		port = ""
	}
	if port != "" {
		host = net.JoinHostPort(host, port)
	} else if strings.Contains(host, ":") {
		host = "[" + host + "]"
	}
	u.Host = host
	// HA base URL paths are case sensitive. Preserve their spelling.
	return strings.TrimRight(u.String(), "/")
}

func lockRuntime(ctx context.Context, target string) (func(), error) {
	return acquireRuntimeLock(ctx, runtimeKey(target))
}
