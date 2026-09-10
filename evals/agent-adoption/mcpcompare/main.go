// mcpcompare compares CLI and MCP results on prepared fixtures.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"time"

	"github.com/BrianTillman/Denmother/internal/operator"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type measurement struct {
	Case            string          `json:"case"`
	Tool            string          `json:"tool"`
	CLIStatus       operator.Status `json:"cli_status"`
	MCPStatus       operator.Status `json:"mcp_status"`
	Parity          bool            `json:"parity"`
	CLIMilliseconds int64           `json:"cli_milliseconds"`
	MCPMilliseconds int64           `json:"mcp_milliseconds"`
	CLIBytes        int             `json:"cli_bytes"`
	MCPBytes        int             `json:"mcp_bytes"`
}
type report struct {
	Kind                            string        `json:"kind"`
	Client                          string        `json:"client"`
	StartedAt                       time.Time     `json:"started_at"`
	DurationMilliseconds            int64         `json:"duration_milliseconds"`
	Calls                           int           `json:"tool_calls"`
	ResourceReads                   int           `json:"resource_reads"`
	EvidenceBytes                   int           `json:"evidence_bytes"`
	FirstVerifiedResultMilliseconds *int64        `json:"time_to_first_verified_result_ms"`
	ManualCorrections               *int          `json:"manual_corrections"`
	FirstSuccessfulTaskMilliseconds *int64        `json:"time_to_first_successful_task_ms"`
	AgentTaskCompletion             any           `json:"agent_task_completion"`
	Note                            string        `json:"note"`
	Results                         []measurement `json:"results"`
}

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
func run() error {
	binary := flag.String("dm", "", "absolute dm binary")
	directory := flag.String("directory", "", "fixtures prepared by prepare.py")
	output := flag.String("output", "", "optional JSON report file")
	flag.Parse()
	if *binary == "" || *directory == "" {
		return fmt.Errorf("--dm and --directory are required")
	}
	executable, err := filepath.Abs(*binary)
	if err != nil {
		return err
	}
	started := time.Now()
	r := report{Kind: "deterministic_transport_parity", Client: "official Go MCP SDK v1.7.0 (stdio)", StartedAt: started.UTC(), Note: "No model session was run. Agent completion and manual corrections require independent rubric/transcript review. A protocol connection is not adoption success."}
	allPassed := true
	for _, id := range []string{"broken-entity", "author-test", "trace-failure", "incomplete-verification"} {
		root, err := filepath.Abs(filepath.Join(*directory, id))
		if err != nil {
			return err
		}
		if _, err := os.Stat(filepath.Join(root, "TASK.md")); err != nil {
			return fmt.Errorf("prepare fixtures first: %w", err)
		}
		ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
		args := []string{"mcp", "--project-root", root}
		if id == "trace-failure" {
			args = append(args, "--evidence-file", "artifacts/failure.json")
		}
		command := exec.Command(executable, args...)
		command.Dir = root
		command.Env = environment()
		command.Stderr = os.Stderr
		client := mcp.NewClient(&mcp.Implementation{Name: "Denmother evaluation comparison", Version: "1"}, nil)
		session, err := client.Connect(ctx, &mcp.CommandTransport{Command: command, TerminateDuration: 40 * time.Second}, nil)
		if err != nil {
			cancel()
			return err
		}
		for _, task := range []struct {
			name string
			args []string
		}{{"project_context", []string{"agent", "context", "--no-schema"}}, {"validate_config", []string{"--no-schema"}}, {"plan_tests", []string{"test", "plan"}}, {"review_change", []string{"agent", "review"}}} {
			cliArgs := append(append([]string{}, task.args...), "--config="+filepath.Join(root, "ha-config"), "--json")
			cli := exec.CommandContext(ctx, executable, cliArgs...)
			cli.Dir = root
			cli.Env = environment()
			before := time.Now()
			cliData, cliErr := cli.Output()
			cliElapsed := time.Since(before).Milliseconds()
			if cliErr != nil {
				if _, ok := cliErr.(*exec.ExitError); !ok {
					session.Close()
					cancel()
					return cliErr
				}
			}
			var want operator.Result
			if err := json.Unmarshal(cliData, &want); err != nil {
				session.Close()
				cancel()
				return err
			}
			before = time.Now()
			got, err := session.CallTool(ctx, &mcp.CallToolParams{Name: task.name, Arguments: map[string]any{}})
			elapsed := time.Since(before).Milliseconds()
			if err != nil {
				session.Close()
				cancel()
				return err
			}
			data, _ := json.Marshal(got.StructuredContent)
			wire, _ := json.Marshal(got)
			var actual operator.Result
			if err := json.Unmarshal(data, &actual); err != nil {
				session.Close()
				cancel()
				return err
			}
			parity := want.Status == actual.Status && want.ExitCode == actual.ExitCode && want.ErrorCode == actual.ErrorCode && reflect.DeepEqual(want.Steps, actual.Steps)
			allPassed = allPassed && parity
			r.Calls++
			r.EvidenceBytes += len(wire)
			if actual.Status == operator.StatusSuccess && r.FirstVerifiedResultMilliseconds == nil {
				ms := time.Since(started).Milliseconds()
				r.FirstVerifiedResultMilliseconds = &ms
			}
			r.Results = append(r.Results, measurement{id, task.name, want.Status, actual.Status, parity, cliElapsed, elapsed, len(cliData), len(wire)})
		}
		if id == "trace-failure" {
			resources, err := session.ListResources(ctx, nil)
			if err != nil {
				session.Close()
				cancel()
				return err
			}
			found := false
			for _, resource := range resources.Resources {
				if resource.Name != "Configured evidence 1" {
					continue
				}
				evidence, err := session.ReadResource(ctx, &mcp.ReadResourceParams{URI: resource.URI})
				if err != nil {
					session.Close()
					cancel()
					return err
				}
				encoded, _ := json.Marshal(evidence)
				r.EvidenceBytes += len(encoded)
				r.ResourceReads++
				found = strings.Contains(string(encoded), "fresh-failure") && strings.Contains(string(encoded), "action/0")
			}
			allPassed = allPassed && found
		}
		if err := session.Close(); err != nil {
			cancel()
			return err
		}
		cancel()
	}
	r.DurationMilliseconds = time.Since(started).Milliseconds()
	data, err := json.MarshalIndent(r, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	if *output != "" {
		if err := os.WriteFile(*output, data, 0600); err != nil {
			return err
		}
	}
	fmt.Print(string(data))
	if !allPassed {
		return fmt.Errorf("CLI/MCP evidence parity failed")
	}
	return nil
}
func environment() []string {
	var env []string
	for _, key := range []string{"PATH", "HOME", "USERPROFILE", "SYSTEMROOT", "TMPDIR", "TMP", "TEMP"} {
		if v, ok := os.LookupEnv(key); ok {
			env = append(env, key+"="+v)
		}
	}
	return env
}
