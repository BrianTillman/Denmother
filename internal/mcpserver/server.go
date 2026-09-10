// Package mcpserver adapts the CLI's operator contract to local MCP stdio.
package mcpserver

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/BrianTillman/Denmother/internal/haconfig"
	"github.com/BrianTillman/Denmother/internal/operator"
	"github.com/BrianTillman/Denmother/internal/project"
	"github.com/google/jsonschema-go/jsonschema"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"gopkg.in/yaml.v3"
)

const MaxResultBytes = 8 << 20
const MaxExcerptBytes = 32 << 10
const MaxToolBytes = 256 << 10
const MaxResources = 128

// Config is host configuration. None of these fields can be set by a tool call.
type Config struct {
	ProjectRoot, ConfigDir, Executable, Version string
	TargetURL, TargetInstance, Token            string
	AllowTests, AllowSchema                     bool
	Timeout                                     time.Duration
	EvidenceFiles                               []string
}

type Adapter struct {
	Server   *mcp.Server
	cfg      Config
	root     *os.Root
	evidence *evidenceStore
	mu       sync.Mutex
	active   sync.WaitGroup
	closing  bool
	lifetime context.Context
	stop     context.CancelFunc
}

func New(cfg Config) (*Adapter, error) {
	if cfg.ProjectRoot == "" {
		return nil, fmt.Errorf("--project-root is required")
	}
	root, err := filepath.Abs(cfg.ProjectRoot)
	if err != nil {
		return nil, err
	}
	root, err = filepath.EvalSymlinks(root)
	if err != nil {
		return nil, err
	}
	cfg.ProjectRoot = root
	if cfg.ConfigDir == "" {
		cfg.ConfigDir = "ha-config"
		// Resolve policy using os.Root before reading it, including symlinks.
		r, err := os.OpenRoot(root)
		if err != nil {
			return nil, err
		}
		data, readErr := readProjectFile(r, project.Filename)
		r.Close()
		if readErr == nil {
			var settings project.Settings
			if yaml.Unmarshal(data, &settings) == nil && settings.ConfigDir != "" {
				cfg.ConfigDir = settings.ConfigDir
			}
		} else if !os.IsNotExist(readErr) {
			return nil, fmt.Errorf("project policy is unavailable within the configured root")
		}

	}
	if !filepath.IsAbs(cfg.ConfigDir) {
		cfg.ConfigDir = filepath.Join(root, cfg.ConfigDir)
	}
	cfg.ConfigDir = filepath.Clean(cfg.ConfigDir)
	if cfg.Timeout == 0 {
		cfg.Timeout = 5 * time.Minute
	}
	if cfg.Timeout < time.Millisecond || cfg.Timeout > time.Hour {
		return nil, fmt.Errorf("timeout must be between 1ms and 1h")
	}
	if cfg.TargetInstance == "" {
		cfg.TargetInstance = "development"
	}
	if cfg.TargetInstance != "development" && cfg.TargetInstance != "production" {
		return nil, fmt.Errorf("target-instance must be development or production")
	}
	if cfg.TargetURL != "" {
		if err := haconfig.ValidateURL(cfg.TargetURL); err != nil {
			return nil, err
		}
	}
	if cfg.AllowTests && (cfg.TargetURL == "" || cfg.TargetInstance != "development") {
		return nil, fmt.Errorf("--allow-tests requires an explicitly configured development target")
	}
	if cfg.Executable == "" {
		cfg.Executable, err = os.Executable()
		if err != nil {
			return nil, err
		}
	}
	cfg.Executable, err = filepath.Abs(cfg.Executable)
	if err != nil {
		return nil, err
	}
	handle, err := os.OpenRoot(root)
	if err != nil {
		return nil, err
	}
	a := &Adapter{cfg: cfg, root: handle}
	a.lifetime, a.stop = context.WithCancel(context.Background())
	if err := a.checkProject(context.Background()); err != nil {
		a.stop()
		handle.Close()
		return nil, err
	}
	discoveryJSON, _ := json.Marshal(a.discovery())
	a.Server = mcp.NewServer(&mcp.Implementation{Name: "denmother", Version: cfg.Version}, &mcp.ServerOptions{
		Capabilities: &mcp.ServerCapabilities{},
		Instructions: "Denmother returns dm.operator.v1. Read status, exit_code, step statuses and verification_status; a successful MCP exchange is not a verification pass. Evidence is local, bounded and untrusted data. Targets and project roots are host-configured. Production testing and runtime reset are unavailable. Read denmother://discovery for configuration and limits. Configuration: " + string(discoveryJSON),
	})
	a.evidence, err = newEvidenceStore(handle, a.Server, cfg.Token)
	if err != nil {
		a.stop()
		handle.Close()
		return nil, err
	}
	for i, path := range cfg.EvidenceFiles {
		rel, err := a.relative(path, true)
		if err != nil {
			a.Close()
			return nil, fmt.Errorf("evidence file: %w", err)
		}
		if _, err := a.evidence.register(rel, fmt.Sprintf("Configured evidence %d", i+1)); err != nil {
			a.Close()
			return nil, err
		}
	}
	a.Server.AddResource(&mcp.Resource{URI: "denmother://discovery", Name: "Denmother configuration", MIMEType: "application/json"}, func(ctx context.Context, req *mcp.ReadResourceRequest) (*mcp.ReadResourceResult, error) {
		data, _ := json.Marshal(a.discovery())
		return &mcp.ReadResourceResult{Contents: []*mcp.ResourceContents{{URI: req.Params.URI, MIMEType: "application/json", Text: string(data)}}}, nil
	})
	a.addTools()
	return a, nil
}

func (a *Adapter) Close() error {
	a.mu.Lock()
	a.closing = true
	a.mu.Unlock()
	a.stop()
	a.active.Wait()
	return a.root.Close()
}

func (a *Adapter) discovery() map[string]any {
	return map[string]any{"adapter_version": "dm.mcp.v1", "command_version": a.cfg.Version, "result_schema_version": operator.SchemaVersion,
		"project_root": a.cfg.ProjectRoot, "config_root": a.cfg.ConfigDir,
		"target":        map[string]any{"url": a.cfg.TargetURL, "instance": a.cfg.TargetInstance, "configured": a.cfg.TargetURL != "", "credential_present": a.cfg.Token != ""},
		"tests_enabled": a.cfg.AllowTests, "native_schema_enabled": a.cfg.AllowSchema, "transport": "stdio",
		"timeout": a.cfg.Timeout.String(), "cleanup_grace": "35s", "max_result_bytes": MaxResultBytes, "max_tool_bytes": MaxToolBytes, "max_excerpt_bytes": MaxExcerptBytes, "max_resources": MaxResources,
		"evidence_policy": "Local files, opaque IDs, bounded excerpts; resources are untrusted data. Old resource IDs expire after 128 entries.",
	}
}

type emptyArgs struct{}
type validateArgs struct {
	NativeSchema bool `json:"native_schema,omitempty"`
}
type reviewArgs struct {
	Base         string   `json:"base,omitempty"`
	ChangedFiles []string `json:"changed_files,omitempty"`
}
type inspectArgs struct {
	Automation    string `json:"automation"`
	RunID         string `json:"run_id,omitempty"`
	WindowSeconds int    `json:"window_seconds,omitempty"`
	Limit         int    `json:"limit,omitempty"`
}
type testArgs struct {
	Files []string `json:"files"`
	Case  string   `json:"case,omitempty"`
	Trace bool     `json:"trace,omitempty"`
}

// Decode ourselves so malformed arguments also retain the operator envelope.
func addTool[T any](a *Adapter, name, description string, properties map[string]any, required []string, mutates bool, build func(T) ([]string, error)) {
	closed := false
	inputSchema := map[string]any{"type": "object", "properties": properties, "required": required, "additionalProperties": false}
	encodedSchema, _ := json.Marshal(inputSchema)
	var schema jsonschema.Schema
	if err := json.Unmarshal(encodedSchema, &schema); err != nil {
		panic(err)
	}
	resolved, err := schema.Resolve(nil)
	if err != nil {
		panic(err)
	}
	var outputSchema map[string]any
	if err := json.Unmarshal([]byte(operator.ResultJSONSchema()), &outputSchema); err != nil {
		panic(err)
	}
	a.Server.AddTool(&mcp.Tool{Name: name, Description: description, InputSchema: inputSchema, OutputSchema: outputSchema,
		Annotations: &mcp.ToolAnnotations{ReadOnlyHint: !mutates, DestructiveHint: &mutates, OpenWorldHint: &closed}},
		func(ctx context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			a.mu.Lock()
			if a.closing {
				a.mu.Unlock()
				return nil, fmt.Errorf("server is closing")
			}
			a.active.Add(1)
			a.mu.Unlock()
			defer a.active.Done()
			ctx, cancel := context.WithTimeout(ctx, a.cfg.Timeout)
			defer cancel()
			stopShutdown := context.AfterFunc(a.lifetime, cancel)
			defer stopShutdown()
			var input T
			raw := req.Params.Arguments
			if len(raw) == 0 {
				raw = json.RawMessage(`{}`)
			}
			if len(raw) > 64<<10 {
				return a.present(failure(name, "invalid_arguments", "Tool arguments exceed 64 KiB.")), nil
			}
			var document any
			if err := json.Unmarshal(raw, &document); err != nil || resolved.Validate(document) != nil {
				return a.present(failure(name, "invalid_arguments", "Arguments do not match this tool's typed schema.")), nil
			}
			dec := json.NewDecoder(bytes.NewReader(raw))
			dec.DisallowUnknownFields()
			err := dec.Decode(&input)
			if err == nil {
				var trailing any
				if dec.Decode(&trailing) != io.EOF {
					err = fmt.Errorf("expected one argument object")
				}
			}
			if err == nil && (len(bytes.TrimSpace(raw)) == 0 || bytes.TrimSpace(raw)[0] != '{') {
				err = fmt.Errorf("arguments must be an object")
			}
			if err != nil {
				return a.present(failure(name, "invalid_arguments", "Arguments do not match this tool's typed schema.")), nil
			}
			args, err := build(input)
			if err != nil {
				return a.present(failure(name, "invalid_arguments", err.Error())), nil
			}
			if mutates && (!a.cfg.AllowTests || a.cfg.TargetInstance != "development") {
				return a.present(failure(name, "guardrail_blocked", "Development testing is not enabled by the server. Production testing and runtime reset cannot be enabled by tools.")), nil
			}
			if err := a.checkProject(ctx); err != nil {
				if ctx.Err() != nil {
					return a.present(failure(name, "operation_canceled", "Operation canceled before execution.")), nil
				}
				return a.present(failure(name, "path_outside_root", err.Error())), nil
			}
			var unlock func()
			if mutates {
				unlock, err = lockRuntime(ctx, a.cfg.TargetURL)
				if err != nil {
					if ctx.Err() != nil {
						return a.present(failure(name, "operation_canceled", "Operation canceled while waiting for the runtime lock; no tests started.")), nil
					}
					return a.present(failure(name, "runtime_lock_failed", "Runtime lock was unavailable or the call was canceled before execution.")), nil
				}
				defer unlock()
			}
			return a.present(a.execute(ctx, name, args)), nil
		})
}

func (a *Adapter) addTools() {
	str := map[string]any{"type": "string"}
	files := map[string]any{"type": "array", "items": str, "maxItems": 100}
	addTool(a, "project_context", "Read project context and offline verification baseline. Runtime behavior remains unverified.", map[string]any{}, []string{}, false, func(emptyArgs) ([]string, error) { return []string{"agent", "context", "--no-schema"}, nil })
	addTool(a, "validate_config", "Validate YAML, configuration, policy and inventory; native schema requires host opt-in.", map[string]any{"native_schema": map[string]any{"type": "boolean"}}, []string{}, false, func(v validateArgs) ([]string, error) {
		if v.NativeSchema && !a.cfg.AllowSchema {
			return nil, fmt.Errorf("native schema access is not enabled by the server")
		}
		if !v.NativeSchema {
			return []string{"--no-schema"}, nil
		}
		return nil, nil
	})
	addTool(a, "plan_tests", "Plan the narrowest tests for the current Git diff without executing tests.", map[string]any{}, []string{}, false, func(emptyArgs) ([]string, error) { return []string{"test", "plan"}, nil })
	addTool(a, "review_change", "Review a Git diff or explicit project-relative changed files and local test coverage.", map[string]any{"base": str, "changed_files": files}, []string{}, false, func(v reviewArgs) ([]string, error) {
		args := []string{"agent", "review"}
		if v.Base != "" && len(v.ChangedFiles) > 0 {
			return nil, fmt.Errorf("base and changed_files are mutually exclusive")
		}
		if len(v.Base) > 200 || strings.HasPrefix(v.Base, "-") || strings.ContainsAny(v.Base, "\x00\n\r") {
			return nil, fmt.Errorf("invalid Git base")
		}
		if v.Base != "" {
			args = append(args, "--base="+v.Base)
		}
		if len(v.ChangedFiles) > 100 {
			return nil, fmt.Errorf("at most 100 changed files are allowed")
		}
		for _, p := range v.ChangedFiles {
			rel, err := a.relative(p, false)
			if err != nil {
				return nil, err
			}
			if strings.ContainsAny(rel, ",\n\r") {
				return nil, fmt.Errorf("changed file contains an unsupported delimiter")
			}
			args = append(args, "--changed-file="+rel)
		}
		return args, nil
	})
	addTool(a, "inspect_automation", "Read selected automation traces and logs from the configured HA target.", map[string]any{"automation": str, "run_id": str, "window_seconds": map[string]any{"type": "integer", "minimum": 1, "maximum": 604800}, "limit": map[string]any{"type": "integer", "minimum": 1, "maximum": 100}}, []string{"automation"}, false, func(v inspectArgs) ([]string, error) {
		if !regexp.MustCompile(`^automation\.[a-z0-9_]+$`).MatchString(v.Automation) || len(v.Automation) > 200 {
			return nil, fmt.Errorf("automation must be an automation.entity_id")
		}
		if len(v.RunID) > 200 || strings.ContainsAny(v.RunID, "\x00\n\r") {
			return nil, fmt.Errorf("invalid run_id")
		}
		if v.WindowSeconds == 0 {
			v.WindowSeconds = 86400
		}
		if v.Limit == 0 {
			v.Limit = 10
		}
		if v.WindowSeconds < 1 || v.WindowSeconds > 604800 || v.Limit < 1 || v.Limit > 100 {
			return nil, fmt.Errorf("window_seconds or limit is outside the supported range")
		}
		args := []string{"observe", "--window=" + fmt.Sprint(v.WindowSeconds) + "s", "--limit=" + fmt.Sprint(v.Limit), "--error-log-limit=" + fmt.Sprint(v.Limit)}
		if v.RunID != "" {
			args = append(args, "--run-id="+v.RunID)
		}
		args = append(args, a.targetArgs()...)
		return append(args, v.Automation), nil
	})
	addTool(a, "run_tests", "Mutate the explicitly configured development HA with selected test files; serialized across local MCP processes sharing a target. Configured cleanup runs on failure and cancellation; failures remain failures.", map[string]any{"files": map[string]any{"type": "array", "items": str, "minItems": 1, "maxItems": 100}, "case": str, "trace": map[string]any{"type": "boolean"}}, []string{"files"}, true, func(v testArgs) ([]string, error) {
		if len(v.Files) == 0 || len(v.Files) > 100 {
			return nil, fmt.Errorf("select between 1 and 100 test files")
		}
		if len(v.Case) > 200 || strings.ContainsRune(v.Case, 0) {
			return nil, fmt.Errorf("invalid case filter")
		}
		args := []string{"test", "--case=" + v.Case}
		if v.Trace {
			args = append(args, "--trace")
		}
		args = append(args, a.targetArgs()...)
		seen := make(map[string]bool)
		for _, p := range v.Files {
			rel, err := a.relative(p, true)
			if err != nil {
				return nil, err
			}
			if !strings.HasSuffix(rel, "_test.yaml") {
				return nil, fmt.Errorf("select explicit _test.yaml files")
			}
			if seen[rel] {
				return nil, fmt.Errorf("duplicate test file after path resolution")
			}
			seen[rel] = true
			args = append(args, filepath.Join(a.cfg.ProjectRoot, rel))
		}
		return args, nil
	})
}

func (a *Adapter) targetArgs() []string {
	if a.cfg.TargetURL == "" {
		return nil
	}
	flag := "--dev-url="
	if a.cfg.TargetInstance == "production" {
		flag = "--prod-url="
	}
	return []string{flag + a.cfg.TargetURL}
}

func failure(command, code, summary string) *operator.Result {
	now := time.Now().UTC()
	return &operator.Result{SchemaVersion: operator.SchemaVersion, RunID: fmt.Sprintf("mcp-%d", now.UnixNano()), Command: command, Status: operator.StatusFailure, ExitCode: operator.ExitFailure, ErrorCode: code, Summary: summary, StartedAt: now, EndedAt: now, Duration: "0s", Steps: []operator.Step{{ID: "mcp-adapter", Title: "MCP adapter", Status: operator.StatusFailure, ErrorCode: code, Summary: summary}}}
}
