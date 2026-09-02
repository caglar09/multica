package agent

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestAntigravityStructuredHelpers(t *testing.T) {
	t.Parallel()

	step := &antigravityStreamStepUpdate{
		StepIndex: 7,
		StepType:  "tool",
		ToolName:  "run_command",
		ToolInfo: &antigravityStreamToolInfo{
			Parameters: map[string]any{"CommandLine": "go test ./..."},
			Output:     "ok",
		},
	}
	if got := antigravityToolName(step); got != "run_command" {
		t.Fatalf("antigravityToolName = %q, want run_command", got)
	}
	if got := antigravityToolInput(step)["CommandLine"]; got != "go test ./..." {
		t.Fatalf("tool input CommandLine = %#v", got)
	}
	if got := antigravityToolOutput(step); got != "ok" {
		t.Fatalf("antigravityToolOutput = %q, want ok", got)
	}

	step.ToolInfo.Error = &antigravityStreamError{Message: "exit status 1"}
	if got := antigravityToolOutput(step); !strings.Contains(got, "exit status 1") {
		t.Fatalf("tool error missing from output: %q", got)
	}

	if got := antigravityResultStatus("SUCCESS"); got != "completed" {
		t.Fatalf("SUCCESS status = %q, want completed", got)
	}
	if got := antigravityResultStatus("INTERRUPTED"); got != "aborted" {
		t.Fatalf("INTERRUPTED status = %q, want aborted", got)
	}
	if got := antigravityResultStatus("ERROR"); got != "failed" {
		t.Fatalf("ERROR status = %q, want failed", got)
	}
}

func TestAntigravityUsageMap(t *testing.T) {
	t.Parallel()

	got := antigravityUsageMap("gemini-3.6-flash-high", antigravityStreamUsage{
		InputTokens:     11,
		OutputTokens:    7,
		ThinkingTokens:  3,
		CacheReadTokens: 5,
		TotalTokens:     26,
	})
	usage, ok := got["gemini-3.6-flash-high"]
	if !ok {
		t.Fatalf("model usage missing: %#v", got)
	}
	if usage.InputTokens != 11 || usage.OutputTokens != 7 || usage.CacheReadTokens != 5 {
		t.Fatalf("unexpected usage mapping: %#v", usage)
	}
}

func fakeAgyStructuredStreamScript() string {
	return "#!/bin/sh\n" +
		"echo '{\"event\":\"init\",\"conversation_id\":\"b8b263a4-4b2f-4339-acc9-78b248e2b606\"}'\n" +
		"echo '{\"event\":\"step_update\",\"step_update\":{\"conversation_id\":\"b8b263a4-4b2f-4339-acc9-78b248e2b606\",\"step_index\":1,\"state\":\"ACTIVE\",\"step_type\":\"agent_response\",\"text_delta\":\"I will test. \"}}'\n" +
		"echo '{\"event\":\"step_update\",\"step_update\":{\"conversation_id\":\"b8b263a4-4b2f-4339-acc9-78b248e2b606\",\"step_index\":2,\"state\":\"ACTIVE\",\"step_type\":\"tool\",\"tool_name\":\"run_command\",\"tool_info\":{\"name\":\"run_command\",\"parameters\":{\"CommandLine\":\"go test ./...\"}}}}'\n" +
		"echo '{\"event\":\"step_update\",\"step_update\":{\"conversation_id\":\"b8b263a4-4b2f-4339-acc9-78b248e2b606\",\"step_index\":2,\"state\":\"DONE\",\"step_type\":\"tool\",\"tool_name\":\"run_command\",\"tool_info\":{\"name\":\"run_command\",\"parameters\":{\"CommandLine\":\"go test ./...\"},\"output\":\"ok\"},\"usage\":{\"input_tokens\":13,\"output_tokens\":4,\"cache_read_tokens\":2,\"total_tokens\":19}}}'\n" +
		"echo '{\"event\":\"step_update\",\"step_update\":{\"conversation_id\":\"b8b263a4-4b2f-4339-acc9-78b248e2b606\",\"step_index\":3,\"state\":\"DONE\",\"step_type\":\"agent_response\",\"text_delta\":\"Done.\"}}'\n" +
		"echo '{\"event\":\"result\",\"result\":{\"conversation_id\":\"b8b263a4-4b2f-4339-acc9-78b248e2b606\",\"status\":\"SUCCESS\",\"response\":\"I will test. Done.\",\"usage\":{\"input_tokens\":99,\"output_tokens\":99,\"cache_read_tokens\":99,\"total_tokens\":297}}}'\n" +
		"exit 0\n"
}

func TestAntigravityBackendStreamsStructuredEvents(t *testing.T) {
	t.Parallel()

	fakePath := filepath.Join(t.TempDir(), "agy")
	writeTestExecutable(t, fakePath, []byte(fakeAgyStructuredStreamScript()))

	backend, err := New("antigravity", Config{ExecutablePath: fakePath, Logger: quietAntigravityLogger()})
	if err != nil {
		t.Fatalf("new antigravity backend: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	session, err := backend.Execute(ctx, "prompt-ignored", ExecOptions{})
	if err != nil {
		t.Fatalf("execute: %v", err)
	}

	var messages []Message
	for msg := range session.Messages {
		messages = append(messages, msg)
	}

	var result Result
	select {
	case result = <-session.Result:
	case <-time.After(10 * time.Second):
		t.Fatal("timeout waiting for result")
	}

	if result.Status != "completed" {
		t.Fatalf("status = %q, error=%q", result.Status, result.Error)
	}
	if result.Output != "I will test. Done." {
		t.Fatalf("output = %q", result.Output)
	}
	if result.SessionID != "b8b263a4-4b2f-4339-acc9-78b248e2b606" {
		t.Fatalf("session id = %q", result.SessionID)
	}

	usage := result.Usage["unknown"]
	if usage.InputTokens != 13 || usage.OutputTokens != 4 || usage.CacheReadTokens != 2 {
		t.Fatalf("unexpected per-turn usage: %#v", usage)
	}

	var sawToolUse, sawToolResult, sawPinnedSession bool
	var text strings.Builder
	for _, msg := range messages {
		switch msg.Type {
		case MessageStatus:
			if msg.SessionID == result.SessionID {
				sawPinnedSession = true
			}
		case MessageText:
			text.WriteString(msg.Content)
		case MessageToolUse:
			if msg.Tool == "run_command" && msg.CallID == "antigravity-step-2" {
				sawToolUse = true
			}
		case MessageToolResult:
			if msg.Tool == "run_command" && msg.CallID == "antigravity-step-2" && msg.Output == "ok" {
				sawToolResult = true
			}
		}
	}
	if !sawPinnedSession || !sawToolUse || !sawToolResult {
		t.Fatalf("missing structured messages: pinned=%v tool_use=%v tool_result=%v messages=%#v",
			sawPinnedSession, sawToolUse, sawToolResult, messages)
	}
	if got := text.String(); got != result.Output {
		t.Fatalf("streamed text = %q, result output = %q", got, result.Output)
	}
}

func fakeAgyMissingResultStreamScript() string {
	return "#!/bin/sh\n" +
		"echo '{\"event\":\"init\",\"conversation_id\":\"b8b263a4-4b2f-4339-acc9-78b248e2b606\"}'\n" +
		"echo '{\"event\":\"step_update\",\"step_update\":{\"conversation_id\":\"b8b263a4-4b2f-4339-acc9-78b248e2b606\",\"step_index\":1,\"state\":\"DONE\",\"step_type\":\"agent_response\",\"text_delta\":\"partial\"}}'\n" +
		"exit 0\n"
}

func TestAntigravityBackendFailsClosedWithoutResultEvent(t *testing.T) {
	t.Parallel()

	fakePath := filepath.Join(t.TempDir(), "agy")
	writeTestExecutable(t, fakePath, []byte(fakeAgyMissingResultStreamScript()))

	backend, err := New("antigravity", Config{ExecutablePath: fakePath, Logger: quietAntigravityLogger()})
	if err != nil {
		t.Fatalf("new antigravity backend: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	session, err := backend.Execute(ctx, "prompt-ignored", ExecOptions{})
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	for range session.Messages {
	}
	result := <-session.Result
	if result.Status != "failed" {
		t.Fatalf("status = %q, want failed", result.Status)
	}
	if !strings.Contains(result.Error, "without a terminal result event") {
		t.Fatalf("unexpected error: %q", result.Error)
	}
}
