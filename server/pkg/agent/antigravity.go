package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

// antigravityBackend implements Backend by spawning Google's Antigravity CLI
// in one-shot print mode. Antigravity CLI 1.1.8+ exposes a machine-readable
// NDJSON stream via --output-format stream-json; this backend consumes that
// stream and normalizes init, response, tool, and result events into Multica's
// provider-neutral Message/Result contract. A legacy plain-text fallback is
// intentionally retained for defensive compatibility with wrappers/test
// fixtures, but registered Antigravity runtimes are version-gated to 1.1.8+.
//
// agy 1.0.14's print mode regressed this stdout contract: a turn can run tools
// and produce a final reply while emitting ZERO bytes to stdout (the log shows
// "PlannerResponse without ModifiedResponse encountered"). Exit code is 0 and
// no error is logged, so a blank-but-"completed" run reaches the daemon and the
// user sees an empty result even though the work happened (MUL-3726, #4595).
// When stdout comes back empty on an otherwise-completed turn, the backend
// therefore recovers the assistant text agy durably wrote to its per-
// conversation transcript (see readAntigravityTranscriptOutput).
//
// Session resumption uses `--conversation <id>`. stream-json exposes the
// conversation id in-band, so the backend pins it as soon as init/step events
// arrive. The daemon-owned log parser remains as a best-effort fallback for
// wrappers and older failure modes that end before a structured init event.
type antigravityBackend struct {
	cfg Config
}

type antigravityStreamEvent struct {
	Event          string                       `json:"event"`
	ConversationID string                       `json:"conversation_id,omitempty"`
	StepUpdate     *antigravityStreamStepUpdate `json:"step_update,omitempty"`
	Result         *antigravityStreamResult     `json:"result,omitempty"`
}

type antigravityStreamStepUpdate struct {
	ConversationID string                     `json:"conversation_id"`
	StepIndex      int                        `json:"step_index"`
	State          string                     `json:"state"`
	StepType       string                     `json:"step_type"`
	ToolName       string                     `json:"tool_name,omitempty"`
	TextDelta      string                     `json:"text_delta,omitempty"`
	Usage          antigravityStreamUsage     `json:"usage,omitempty"`
	ToolInfo       *antigravityStreamToolInfo `json:"tool_info,omitempty"`
	SubagentInfo   map[string]any             `json:"subagent_info,omitempty"`
}

type antigravityStreamToolInfo struct {
	Name       string                  `json:"name"`
	Parameters map[string]any          `json:"parameters,omitempty"`
	Output     string                  `json:"output,omitempty"`
	Error      *antigravityStreamError `json:"error,omitempty"`
}

type antigravityStreamError struct {
	Type    string `json:"type,omitempty"`
	Message string `json:"message,omitempty"`
}

type antigravityStreamUsage struct {
	InputTokens     int64 `json:"input_tokens"`
	OutputTokens    int64 `json:"output_tokens"`
	ThinkingTokens  int64 `json:"thinking_tokens"`
	CacheReadTokens int64 `json:"cache_read_tokens"`
	TotalTokens     int64 `json:"total_tokens"`
}

type antigravityStreamResult struct {
	ConversationID string                 `json:"conversation_id"`
	Status         string                 `json:"status"`
	Response       string                 `json:"response"`
	Error          string                 `json:"error,omitempty"`
	Usage          antigravityStreamUsage `json:"usage,omitempty"`
}

func (u antigravityStreamUsage) empty() bool {
	return u.InputTokens == 0 &&
		u.OutputTokens == 0 &&
		u.ThinkingTokens == 0 &&
		u.CacheReadTokens == 0 &&
		u.TotalTokens == 0
}

func (u *antigravityStreamUsage) add(other antigravityStreamUsage) {
	u.InputTokens += other.InputTokens
	u.OutputTokens += other.OutputTokens
	u.ThinkingTokens += other.ThinkingTokens
	u.CacheReadTokens += other.CacheReadTokens
	u.TotalTokens += other.TotalTokens
}

func antigravityUsageMap(model string, usage antigravityStreamUsage) map[string]TokenUsage {
	if usage.empty() {
		return map[string]TokenUsage{}
	}
	model = strings.TrimSpace(model)
	if model == "" {
		model = "unknown"
	}
	return map[string]TokenUsage{
		model: {
			InputTokens:     usage.InputTokens,
			OutputTokens:    usage.OutputTokens,
			CacheReadTokens: usage.CacheReadTokens,
		},
	}
}

func antigravityToolName(step *antigravityStreamStepUpdate) string {
	if step == nil {
		return "antigravity_tool"
	}
	if strings.TrimSpace(step.ToolName) != "" {
		return step.ToolName
	}
	if step.ToolInfo != nil && strings.TrimSpace(step.ToolInfo.Name) != "" {
		return step.ToolInfo.Name
	}
	if len(step.SubagentInfo) > 0 {
		return "subagent"
	}
	return "antigravity_tool"
}

func antigravityToolInput(step *antigravityStreamStepUpdate) map[string]any {
	if step == nil {
		return nil
	}
	if step.ToolInfo != nil && len(step.ToolInfo.Parameters) > 0 {
		return step.ToolInfo.Parameters
	}
	if len(step.SubagentInfo) > 0 {
		return map[string]any{"subagent_info": step.SubagentInfo}
	}
	return nil
}

func antigravityToolOutput(step *antigravityStreamStepUpdate) string {
	if step == nil {
		return ""
	}
	if step.ToolInfo != nil {
		output := step.ToolInfo.Output
		if step.ToolInfo.Error != nil {
			errText := strings.TrimSpace(step.ToolInfo.Error.Message)
			if errText == "" {
				errText = strings.TrimSpace(step.ToolInfo.Error.Type)
			}
			if errText != "" {
				if output != "" && !strings.HasSuffix(output, "\n") {
					output += "\n"
				}
				output += "error: " + errText
			}
		}
		return output
	}
	if len(step.SubagentInfo) > 0 {
		if raw, err := json.Marshal(step.SubagentInfo); err == nil {
			return string(raw)
		}
	}
	return ""
}

func antigravityResultStatus(status string) string {
	switch strings.ToUpper(strings.TrimSpace(status)) {
	case "SUCCESS":
		return "completed"
	case "CANCELED", "INTERRUPTED":
		return "aborted"
	default:
		return "failed"
	}
}

func (b *antigravityBackend) Execute(ctx context.Context, prompt string, opts ExecOptions) (*Session, error) {
	execPath := b.cfg.ExecutablePath
	if execPath == "" {
		execPath = "agy"
	}
	if _, err := exec.LookPath(execPath); err != nil {
		return nil, fmt.Errorf("agy executable not found at %q: %w", execPath, err)
	}

	// Guard against agy's silent no-op on an unrecognised --model: it exits 0
	// with empty output, which would otherwise surface as a "completed" but
	// empty task. opts.Model is the single funnel for both agent.model and the
	// daemon-wide MULTICA_ANTIGRAVITY_MODEL default (resolved in daemon.go), so
	// validating it here covers every source — UI free-text, API, a persisted
	// value, and the env default alike. Reject a non-empty model the installed
	// CLI definitively does not advertise, with an actionable error. Validation
	// is fail-OPEN: if the `agy models` catalog can't be discovered we let agy
	// resolve the value itself rather than blocking the run on a discovery
	// hiccup (see antigravityModelError).
	if opts.Model != "" {
		catalog, _ := ListModels(ctx, "antigravity", b.cfg.commandAt(execPath))
		if err := antigravityModelError(opts.Model, catalog.Models); err != nil {
			return nil, err
		}
	}

	timeout := opts.Timeout
	runCtx, cancel := runContext(ctx, timeout)

	logFile, err := os.CreateTemp("", "multica-agy-log-*.log")
	if err != nil {
		cancel()
		return nil, fmt.Errorf("create agy log file: %w", err)
	}
	logPath := logFile.Name()
	_ = logFile.Close()

	args := buildAntigravityArgs(prompt, logPath, timeout, opts, b.cfg.Logger)

	cmd := b.cfg.commandAt(execPath).exec(runCtx, args...)
	hideAgentWindow(cmd)
	b.cfg.logAgentCommand(cmd, newAgentCommandLogArgs(args))
	cmd.WaitDelay = 10 * time.Second
	if opts.Cwd != "" {
		cmd.Dir = opts.Cwd
	}
	cmd.Env = buildEnv(b.cfg.Env)

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		cancel()
		_ = os.Remove(logPath)
		return nil, fmt.Errorf("agy stdout pipe: %w", err)
	}
	stderrBuf := newStderrTail(newLogWriter(b.cfg.Logger, "[agy:stderr] "), agentStderrTailBytes)
	cmd.Stderr = stderrBuf

	if err := startOwnedProcessTree(cmd, b.cfg.Logger); err != nil {
		cancel()
		_ = os.Remove(logPath)
		return nil, fmt.Errorf("start agy: %w", err)
	}

	b.cfg.Logger.Info("agy started", "pid", cmd.Process.Pid, "cwd", opts.Cwd, "model", opts.Model)

	msgCh := make(chan Message, 256)
	resCh := make(chan Result, 1)

	go func() {
		<-runCtx.Done()
		_ = stdout.Close()
	}()

	go func() {
		defer cancel()
		defer close(msgCh)
		defer close(resCh)
		defer os.Remove(logPath)

		startTime := time.Now()
		var legacyOutput strings.Builder
		var streamedOutput strings.Builder
		var resultResponse string
		var resultUsage antigravityStreamUsage
		var turnUsage antigravityStreamUsage
		var hasTurnUsage bool
		finalStatus := "completed"
		var finalError string
		var sessionID string
		var sawStructuredEvent bool
		var sawResult bool
		toolUseSent := map[int]bool{}
		toolResultSent := map[int]bool{}
		usageRecorded := map[int]bool{}

		scanner := newAgentStreamScanner(stdout)

		trySend(msgCh, Message{Type: MessageStatus, Status: "running"})

		pinSession := func(id string) {
			id = strings.TrimSpace(id)
			if id == "" || sessionID == id {
				return
			}
			sessionID = id
			trySend(msgCh, Message{Type: MessageStatus, Status: "running", SessionID: id})
		}

		for scanner.Scan() {
			line := scanner.Text()
			var event antigravityStreamEvent
			if err := json.Unmarshal([]byte(line), &event); err == nil && event.Event != "" {
				sawStructuredEvent = true
				switch event.Event {
				case "init":
					pinSession(event.ConversationID)

				case "step_update":
					step := event.StepUpdate
					if step == nil {
						continue
					}
					pinSession(step.ConversationID)

					if strings.EqualFold(step.State, "DONE") && !usageRecorded[step.StepIndex] && !step.Usage.empty() {
						turnUsage.add(step.Usage)
						hasTurnUsage = true
						usageRecorded[step.StepIndex] = true
					}

					switch step.StepType {
					case "agent_response":
						if step.TextDelta != "" {
							streamedOutput.WriteString(step.TextDelta)
							trySend(msgCh, Message{Type: MessageText, Content: step.TextDelta})
						}

					case "tool":
						callID := fmt.Sprintf("antigravity-step-%d", step.StepIndex)
						toolName := antigravityToolName(step)
						if !toolUseSent[step.StepIndex] {
							trySend(msgCh, Message{
								Type:   MessageToolUse,
								Tool:   toolName,
								CallID: callID,
								Input:  antigravityToolInput(step),
							})
							toolUseSent[step.StepIndex] = true
						}
						if strings.EqualFold(step.State, "DONE") && !toolResultSent[step.StepIndex] {
							trySend(msgCh, Message{
								Type:   MessageToolResult,
								Tool:   toolName,
								CallID: callID,
								Output: antigravityToolOutput(step),
							})
							toolResultSent[step.StepIndex] = true
						}
					}

				case "result":
					if event.Result == nil {
						continue
					}
					sawResult = true
					pinSession(event.Result.ConversationID)
					resultResponse = event.Result.Response
					resultUsage = event.Result.Usage
					finalStatus = antigravityResultStatus(event.Result.Status)
					if finalStatus != "completed" {
						finalError = strings.TrimSpace(event.Result.Error)
						if finalError == "" {
							finalError = fmt.Sprintf("agy ended with status %s", event.Result.Status)
						}
					}
				}
				continue
			}

			if sawStructuredEvent {
				// Once the stream contract has been observed, never reinterpret
				// malformed/non-event stdout as assistant text. Structured mode
				// reserves stdout for NDJSON and diagnostics belong on stderr.
				b.cfg.Logger.Warn("agy emitted non-event stdout in stream-json mode", "line_len", len(line))
				continue
			}

			// Defensive legacy fallback for wrappers/test fixtures that still
			// produce plain text. Registered runtimes are version-gated to a
			// stream-json capable agy release, so production should not take
			// this branch.
			chunk := line
			if legacyOutput.Len() > 0 {
				legacyOutput.WriteByte('\n')
				chunk = "\n" + line
			}
			legacyOutput.WriteString(line)
			if chunk != "" {
				trySend(msgCh, Message{Type: MessageText, Content: chunk})
			}
		}
		if err := scanner.Err(); err != nil {
			b.cfg.Logger.Warn("agy stdout scanner error", "err", err)
		}

		waitErr := cmd.Wait()
		releaseProcessGroup(cmd)
		duration := time.Since(startTime)

		if sessionID == "" {
			sessionID = readAntigravityConversationID(logPath)
		}

		if runCtx.Err() == context.DeadlineExceeded {
			finalStatus = "timeout"
			finalError = fmt.Sprintf("agy timed out after %s", timeout)
		} else if runCtx.Err() == context.Canceled {
			finalStatus = "aborted"
			finalError = "execution cancelled"
		} else if waitErr != nil && finalStatus == "completed" {
			finalStatus = "failed"
			finalError = fmt.Sprintf("agy exited with error: %v", waitErr)
		} else if finalStatus == "completed" && antigravityPrintTimedOut(logPath) {
			finalStatus = "timeout"
			finalError = fmt.Sprintf(
				"agy --print-timeout elapsed after %s waiting for the agent response; a long-running command likely outlived the print timeout",
				antigravityPrintTimeout(timeout),
			)
		} else if providerErr := antigravityProviderError(logPath); finalStatus == "completed" && providerErr != "" {
			finalStatus = "failed"
			finalError = fmt.Sprintf("agy provider error: %s", providerErr)
		} else if sawStructuredEvent && !sawResult && finalStatus == "completed" {
			finalStatus = "failed"
			finalError = "agy stream-json output ended without a terminal result event"
		}
		if finalError != "" {
			finalError = withAgentStderr(finalError, "agy", stderrBuf.Tail())
		}

		finalOutput := legacyOutput.String()
		if sawStructuredEvent {
			finalOutput = resultResponse
			if finalOutput == "" {
				finalOutput = streamedOutput.String()
			}

			// result.response is authoritative. Usually text_delta already
			// reconstructed it exactly; if no delta arrived (or only a strict
			// prefix arrived), catch the transcript up without duplicating text.
			streamed := streamedOutput.String()
			if finalOutput != "" {
				switch {
				case streamed == "":
					trySend(msgCh, Message{Type: MessageText, Content: finalOutput})
				case strings.HasPrefix(finalOutput, streamed) && len(finalOutput) > len(streamed):
					trySend(msgCh, Message{Type: MessageText, Content: finalOutput[len(streamed):]})
				}
			}
		}

		if finalStatus == "completed" && strings.TrimSpace(finalOutput) == "" {
			// Keep the historical transcript recovery as a final safety net.
			if recovered := readAntigravityTranscriptOutput(logPath, sessionID); recovered != "" {
				finalOutput = recovered
				trySend(msgCh, Message{Type: MessageText, Content: recovered})
				b.cfg.Logger.Info("agy recovered empty stdout from transcript", "bytes", len(recovered))
			}
		}

		usage := resultUsage
		if hasTurnUsage {
			usage = turnUsage
		} else if opts.ResumeSessionID != "" {
			// The terminal usage envelope may be cumulative for a resumed
			// conversation. Without per-step usage, reporting it as this task's
			// usage would double-count prior turns, so fail closed to empty.
			usage = antigravityStreamUsage{}
		}

		b.cfg.Logger.Info("agy finished", "pid", cmd.Process.Pid, "status", finalStatus, "duration", duration.Round(time.Millisecond).String())

		resCh <- Result{
			Status:     finalStatus,
			Output:     finalOutput,
			Error:      finalError,
			DurationMs: duration.Milliseconds(),
			SessionID:  sessionID,
			Usage:      antigravityUsageMap(opts.Model, usage),
		}
	}()

	return &Session{Messages: msgCh, Result: resCh}, nil
}

// antigravityConversationIDRe matches the glog line printmode.go writes when
// the CLI dispatches the user's message — the only place in the log that
// reliably surfaces the conversation UUID for both fresh and resumed turns.
//
// Example: `I0528 13:36:23.318877 73304 printmode.go:130] Print mode:
// conversation=b8b263a4-4b2f-4339-acc9-78b248e2b606, sending message`
var antigravityConversationIDRe = regexp.MustCompile(
	`conversation=([0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12})`,
)

// antigravityPrintTimeoutRe matches the glog line agy's printmode.go writes when
// the print-mode wall-clock budget (--print-timeout) elapses before the agent
// produced a final response. agy then prints "Error: timed out waiting for
// response" to stdout and EXITS 0 — runCtx never trips and cmd.Wait returns nil
// — so without this signal the daemon would record the truncated turn as a
// successful "completed" (MUL-3570).
//
// Example: `E0623 17:17:59.017212 65926 printmode.go:289] Print mode: timed out
// after 100 polls (printed=3)`
var antigravityPrintTimeoutRe = regexp.MustCompile(`Print mode: timed out after \d+ polls`)

var antigravityProviderErrorRe = regexp.MustCompile(`agent executor error:\s*(.+)`)

// antigravityPrintTimedOut reports whether the per-run log shows agy hit its own
// print-mode timeout. Best-effort: returns false if the log is missing or the
// marker format changes upstream, in which case the run is classified by its
// exit status as before.
func antigravityPrintTimedOut(logPath string) bool {
	if logPath == "" {
		return false
	}
	data, err := os.ReadFile(logPath)
	if err != nil {
		return false
	}
	return antigravityPrintTimeoutRe.Match(data)
}

// antigravityProviderError extracts terminal upstream/model errors that agy logs
// but does not necessarily print to stdout or reflect in its exit code.
func antigravityProviderError(logPath string) string {
	if logPath == "" {
		return ""
	}
	data, err := os.ReadFile(logPath)
	if err != nil {
		return ""
	}
	matches := antigravityProviderErrorRe.FindAllSubmatch(data, -1)
	if len(matches) == 0 {
		return ""
	}
	return strings.TrimSpace(string(matches[len(matches)-1][1]))
}

// readAntigravityConversationID scans the per-run log file for the
// conversation UUID. Best-effort: returns "" if the log file is missing, the
// CLI exited before dispatching, or the format changes upstream.
func readAntigravityConversationID(logPath string) string {
	if logPath == "" {
		return ""
	}
	data, err := os.ReadFile(logPath)
	if err != nil {
		return ""
	}
	matches := antigravityConversationIDRe.FindAllSubmatch(data, -1)
	if len(matches) == 0 {
		return ""
	}
	// The CLI logs the conversation id repeatedly during a turn (one entry
	// per dispatched message, plus stream-update lines). Any non-empty UUID
	// in the file resolves to the same conversation, so the last match wins
	// — that's what `--conversation` should be pinned to next turn.
	return string(matches[len(matches)-1][1])
}

// antigravityAppDataDirRe matches the glog line agy writes at startup naming its
// CLI app data directory — the root under which per-conversation transcripts
// live. Reading the path from the log (which the daemon owns via --log-file)
// is more robust than guessing $HOME, and follows agy through a custom data dir.
//
// Example: `I0630 14:19:40.582492 88197 common.go:156] CLI app data directory:
// /Users/me/.gemini/antigravity-cli`
var antigravityAppDataDirRe = regexp.MustCompile(`CLI app data directory:\s*(.+)`)

// antigravityTranscriptRecord is the minimal shape of one line in agy's
// per-conversation transcript.jsonl. A turn opens with a USER_INPUT record; the
// assistant's replies are PLANNER_RESPONSE records with source=MODEL and (once
// settled) status=DONE. Content holds the text, or JSON null for a tool-only
// step — it is RawMessage so a null or non-string value is skipped rather than
// failing the whole line.
type antigravityTranscriptRecord struct {
	Type    string          `json:"type"`
	Source  string          `json:"source"`
	Status  string          `json:"status"`
	Content json.RawMessage `json:"content"`
}

// readAntigravityTranscriptOutput recovers the assistant's text from agy's
// per-conversation transcript when stdout carried nothing. agy 1.0.14's print
// mode can finish a turn (tools executed, final reply produced) while emitting
// zero bytes to stdout, leaving the daemon with a blank but "completed" run
// (MUL-3726, #4595). The full reply is still durably written to:
//
//	<appDataDir>/brain/<conversation-id>/.system_generated/logs/transcript.jsonl
//
// as PLANNER_RESPONSE / source=MODEL records.
//
// The transcript is per-conversation and ACCUMULATES across resumed turns
// (daemon reuses the conversation via --conversation / ResumeSessionID), so we
// must return only the CURRENT turn's reply — otherwise a later empty-stdout
// turn would re-emit prior turns' answers. Each turn opens with a USER_INPUT
// record, so we reset on every USER_INPUT and keep only the model text that
// follows the last one. We also require status=DONE to skip any future
// streaming/partial planner records. The remaining text is joined in order
// (intermediate narration + final reply), mirroring what stdout would have
// streamed for this turn. Best-effort: returns "" if the app data dir or
// conversation id is unknown, the transcript is missing, or it holds no model
// text for the current turn.
func readAntigravityTranscriptOutput(logPath, conversationID string) string {
	if logPath == "" || conversationID == "" {
		return ""
	}
	appDataDir := readAntigravityAppDataDir(logPath)
	if appDataDir == "" {
		return ""
	}
	transcriptPath := filepath.Join(
		appDataDir, "brain", conversationID, ".system_generated", "logs", "transcript.jsonl",
	)
	f, err := os.Open(transcriptPath)
	if err != nil {
		return ""
	}
	defer f.Close()

	var parts []string
	scanner := newAgentStreamScanner(f)
	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}
		var rec antigravityTranscriptRecord
		if err := json.Unmarshal(line, &rec); err != nil {
			continue
		}
		if rec.Type == "USER_INPUT" {
			// New turn boundary: drop anything collected for prior turns so a
			// resumed conversation yields only the current turn's reply.
			parts = parts[:0]
			continue
		}
		if rec.Type != "PLANNER_RESPONSE" || rec.Source != "MODEL" || rec.Status != "DONE" {
			continue
		}
		var text string
		// Content is JSON null for tool-only steps; unmarshal leaves text "".
		// A non-string value (object) errors and is skipped.
		if err := json.Unmarshal(rec.Content, &text); err != nil {
			continue
		}
		if strings.TrimSpace(text) != "" {
			parts = append(parts, text)
		}
	}
	return strings.Join(parts, "\n\n")
}

// readAntigravityAppDataDir extracts agy's CLI app data directory from the
// per-run log. Best-effort: returns "" if the log is missing or the marker
// format changes upstream.
func readAntigravityAppDataDir(logPath string) string {
	if logPath == "" {
		return ""
	}
	data, err := os.ReadFile(logPath)
	if err != nil {
		return ""
	}
	m := antigravityAppDataDirRe.FindSubmatch(data)
	if m == nil {
		return ""
	}
	return strings.TrimSpace(string(m[1]))
}

// antigravityBlockedArgs are flags hardcoded by the daemon that must not be
// overridden by user-configured custom_args. Overriding these would break
// non-interactive operation or the daemon's session-resume bookkeeping.
var antigravityBlockedArgs = map[string]blockedArgMode{
	"-p":                             blockedWithValue,
	"--print":                        blockedWithValue,
	"--prompt":                       blockedWithValue,
	"-i":                             blockedStandalone, // interactive mode requires a TTY and cannot run under the daemon
	"--prompt-interactive":           blockedStandalone,
	"-c":                             blockedStandalone, // resume via --conversation, not --continue
	"--continue":                     blockedStandalone,
	"--conversation":                 blockedWithValue, // managed via ExecOptions.ResumeSessionID
	"--model":                        blockedWithValue, // managed via ExecOptions.Model / agent.model
	"--print-timeout":                blockedWithValue,
	"--output-format":                blockedWithValue, // daemon owns the NDJSON transport
	"--input-format":                 blockedWithValue, // -p one-shot mode must stay text-input
	"--dangerously-skip-permissions": blockedStandalone, // always-on in daemon mode
	"--log-file":                     blockedWithValue,  // daemon needs it for session capture
	"--settings":                     blockedWithValue,  // Claude Code-only flag; agy rejects it
}

// buildAntigravityArgs assembles the argv for a daemon-compatible one-shot agy
// invocation.
//
//	agy -p <prompt> --dangerously-skip-permissions --output-format stream-json
//	    [--model <catalog id>] --print-timeout <duration> --log-file <tmp>
//	    [--conversation <id>] [--add-dir <cwd>]
//
// agy 1.0.6 added a `--model` flag (MUL-3125), so opts.Model is now wired
// through when set. The value is the catalog identifier parseAntigravityModels
// read out of `agy models`: a slug (e.g. "gemini-3.6-flash-high") on agy
// 1.1.11+, or the verbatim single-column value (e.g. "Claude Opus 4.6
// (Thinking)") on older output. Either shape ships as one exec arg, so spaces
// and parens need no shell quoting. agy still exposes no --system-prompt;
// runtime instructions are delivered via AGENTS.md in the task workdir.
//
// agy silently no-ops on a model string it doesn't recognise (empty output,
// exit 0), so Execute validates opts.Model against the `agy models` catalog
// and rejects an unrecognised value up front (see antigravityModelError) —
// by the time we build argv the value is either empty or known-good. When
// opts.Model is empty we omit the flag and agy resolves its own default.
func buildAntigravityArgs(prompt, logPath string, timeout time.Duration, opts ExecOptions, logger *slog.Logger) []string {
	args := []string{
		"-p", prompt,
		"--dangerously-skip-permissions",
		"--output-format", "stream-json",
	}
	if opts.Model != "" {
		args = append(args, "--model", opts.Model)
	}
	// agy's --print-timeout has NO "disabled" value and DEFAULTS TO 5m when the
	// flag is omitted, so "no cap" cannot be expressed by dropping it — that
	// silently guillotines every turn at 5 minutes, killing any run whose build
	// or tests outlive the budget (MUL-3570). Always pass the flag: the
	// configured wall-clock cap when positive, else a value so large agy's own
	// timeout never fires before the daemon's idle/tool watchdogs reclaim a
	// genuinely stuck run (see antigravityPrintTimeout).
	args = append(args, "--print-timeout", antigravityFormatTimeout(antigravityPrintTimeout(timeout)))
	args = append(args, "--log-file", logPath)
	if opts.ResumeSessionID != "" {
		args = append(args, "--conversation", opts.ResumeSessionID)
	}
	if opts.Cwd != "" {
		args = append(args, "--add-dir", filepath.Clean(opts.Cwd))
	}
	args = append(args, filterCustomArgs(opts.ExtraArgs, antigravityBlockedArgs, logger)...)
	args = append(args, filterCustomArgs(opts.CustomArgs, antigravityBlockedArgs, logger)...)
	return args
}

// antigravityModelError returns an actionable error when `model` is non-empty
// and definitively absent from `available` (the `agy models` catalog); it
// returns nil otherwise. An empty `available` means discovery couldn't produce
// a catalog (agy missing, transient failure) — we fail OPEN there and let agy
// resolve the value, so a discovery hiccup never blocks a run. The match is
// exact because agy's --model wants the precise catalog identifier; a near-miss
// (extra space, dropped suffix) is correctly rejected since agy would silently
// no-op on it anyway.
func antigravityModelError(model string, available []Model) error {
	if model == "" || len(available) == 0 {
		return nil
	}
	ids := make([]string, 0, len(available))
	for _, m := range available {
		if m.ID == model {
			return nil
		}
		ids = append(ids, m.ID)
	}
	return fmt.Errorf(
		"antigravity model %q is not available from `agy models`; pick one of: %s",
		model, strings.Join(ids, ", "),
	)
}

// antigravityNoCapPrintTimeout is the --print-timeout value used when the daemon
// configures no wall-clock cap (opts.Timeout <= 0). agy's --print-timeout has no
// "disabled" sentinel and falls back to a 5-minute default when omitted, so "no
// cap" must instead be a value large enough that agy's own guillotine never
// fires before the daemon's inactivity watchdog reclaims a genuinely stuck run.
// 24h is effectively unbounded for any real turn while still being a finite
// duration agy can parse, and it stays above the watchdog budget even when an
// operator raises MULTICA_AGENT_IDLE_WATCHDOG well past its default.
const antigravityNoCapPrintTimeout = 24 * time.Hour

// antigravityPrintTimeout resolves the wall-clock budget handed to agy's
// --print-timeout: the daemon's configured cap when positive, else the no-cap
// sentinel above. It is the single source of truth shared by
// buildAntigravityArgs (which sets the flag) and Execute (which labels a
// print-mode timeout).
func antigravityPrintTimeout(timeout time.Duration) time.Duration {
	if timeout > 0 {
		return timeout
	}
	return antigravityNoCapPrintTimeout
}

// antigravityFormatTimeout renders a Go duration in the `<n>m<n>s` shape the
// agy CLI accepts (e.g. 20m0s). Sub-second timeouts round up to 1s so the CLI
// doesn't reject the flag.
func antigravityFormatTimeout(d time.Duration) string {
	if d < time.Second {
		d = time.Second
	}
	// time.Duration.String() already produces shapes like "20m0s" / "1h30m0s"
	// that agy parses via Go's stdlib flag.Duration on the receiving side.
	return d.String()
}
