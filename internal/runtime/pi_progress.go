package runtime

import (
	"bufio"
	"encoding/json"
	"io"
)

// Pi --mode json event types and field paths are taken from earendil-works/pi
// v0.84.2: packages/coding-agent/docs/json.md, packages/ai/src/types.ts
// (Usage, AssistantMessage, StopReason), and packages/coding-agent/src/modes/json-event.ts.
// Fixtures under testdata/pi/ are constructed to that schema (not a live capture);
// regenerate with testdata/pi/regen.sh when a recorded run is available.
// Re-verify after pi releases change the wire format (fast cadence: ~weekly
// minors; 0.84.0 changed message_update to delta-only).

// piEnvelope is the common shape of every NDJSON line from pi --mode json.
type piEnvelope struct {
	Type string `json:"type"`
}

// session header — first line; schema version is an int, id is the session UUID.
// There is no model field on the header (model lives on AssistantMessage).
type piSessionEvent struct {
	ID      string `json:"id"`
	Version int    `json:"version"`
}

type piCost struct {
	Total float64 `json:"total"`
}

// piUsage matches packages/ai/src/types.ts Usage (camelCase).
type piUsage struct {
	Input      int    `json:"input"`
	Output     int    `json:"output"`
	CacheRead  int    `json:"cacheRead"`
	CacheWrite int    `json:"cacheWrite"`
	Reasoning  *int   `json:"reasoning"`
	Cost       piCost `json:"cost"`
}

func (u piUsage) reasoningTokens() int {
	if u.Reasoning == nil {
		return 0
	}
	return *u.Reasoning
}

// piWireMessage is the subset of UserMessage | AssistantMessage | ToolResultMessage
// that parsePiStream reads. Unknown roles are ignored.
type piWireMessage struct {
	Role         string  `json:"role"`
	Model        string  `json:"model"`
	Usage        piUsage `json:"usage"`
	StopReason   string  `json:"stopReason"`
	ErrorMessage string  `json:"errorMessage"`
}

type piMessageEvent struct {
	Message piWireMessage `json:"message"`
}

type piDeltaEvent struct {
	Type  string `json:"type"`
	Delta string `json:"delta"`
}

type piMessageUpdateEvent struct {
	AssistantMessageEvent piDeltaEvent `json:"assistantMessageEvent"`
}

type piToolExecutionEndEvent struct {
	ToolName string          `json:"toolName"`
	Result   json.RawMessage `json:"result"`
	IsError  bool            `json:"isError"`
}

type piAgentEndEvent struct {
	Messages  []piWireMessage `json:"messages"`
	WillRetry bool            `json:"willRetry"`
}

func piLastAssistant(messages []piWireMessage) (piWireMessage, bool) {
	for i := len(messages) - 1; i >= 0; i-- {
		if messages[i].Role == "assistant" {
			return messages[i], true
		}
	}
	return piWireMessage{}, false
}

func piResultSummary(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		return redactSummary(s)
	}
	var obj map[string]any
	if err := json.Unmarshal(raw, &obj); err == nil {
		for _, k := range []string{"output", "text", "error", "message"} {
			if v, ok := obj[k].(string); ok && v != "" {
				return redactSummary(v)
			}
		}
	}
	return redactSummary(string(raw))
}

func piIsErrorStop(reason string) bool {
	return reason == "error" || reason == "aborted"
}

// parsePiStream reads NDJSON from pi's --mode json output and emits
// normalized AgentEvent values via the onEvent callback. It returns the
// sessionID captured from the session header's `id` field.
//
// Pi's wire format (v0.84.2):
//   - Session header {type:session, version:3, id, timestamp, cwd} — no model.
//   - Streaming deltas arrive as message_update.assistantMessageEvent
//     (text_delta / thinking_delta). message_end.message is authoritative.
//   - Tool completion is tool_execution_end {toolCallId, toolName, result, isError}.
//   - Usage/cost on AssistantMessage are camelCase; cost is a nested object.
//   - agent_end is {messages, willRetry} — stopReason lives on the assistant
//     message, not on the agent_end envelope. willRetry=true is a checkpoint
//     (retry/compaction may follow); ResultEvent is emitted only when
//     willRetry is false.
//   - Tool names are lowercase (bash, read, write, edit, glob, grep) — the
//     hook adapter translates to Claude-name vocabulary (#608).
//   - --mode json exits 0 on model error (stopReason: error/aborted only
//     maps to exit 1 in text mode) — ParseTranscriptFile must detect errors
//     from the stream, not the exit code.
func parsePiStream(r io.Reader, onEvent func(AgentEvent)) (sessionID string, err error) {
	br := bufio.NewReaderSize(r, streamBufSize)

	var (
		numTurns        int
		totalCostUSD    float64
		totalInput      int
		totalOutput     int
		totalReasoning  int
		totalCacheRead  int
		totalCacheWrite int
		sawError        bool
		lastErrorMsg    string
		lastStopReason  string
		sawAgentEnd     bool
		emittedInit     bool
	)

	emitInit := func(model string) {
		if emittedInit {
			return
		}
		emittedInit = true
		onEvent(InitEvent{Model: model})
	}

	accumulateAssistant := func(msg piWireMessage) {
		if msg.Role != "assistant" {
			return
		}
		emitInit(msg.Model)
		numTurns++
		totalCostUSD += msg.Usage.Cost.Total
		totalInput += msg.Usage.Input
		totalOutput += msg.Usage.Output
		totalReasoning += msg.Usage.reasoningTokens()
		totalCacheRead += msg.Usage.CacheRead
		totalCacheWrite += msg.Usage.CacheWrite
		lastStopReason = msg.StopReason
		if msg.ErrorMessage != "" {
			lastErrorMsg = redactSummary(msg.ErrorMessage)
		}
		if piIsErrorStop(msg.StopReason) {
			sawError = true
			onEvent(ErrorEvent{
				ErrorType: msg.StopReason,
				Message:   lastErrorMsg,
			})
		}
		onEvent(TokensEvent{
			InputTokens:     msg.Usage.Input,
			OutputTokens:    msg.Usage.Output,
			ReasoningTokens: msg.Usage.reasoningTokens(),
			CacheRead:       msg.Usage.CacheRead,
			CacheWrite:      msg.Usage.CacheWrite,
		})
	}

	for {
		line, isPrefix, err := br.ReadLine()
		if err == io.EOF {
			break
		}
		if err != nil {
			return sessionID, err
		}
		// Skip lines exceeding the buffer (same pattern as other parsers).
		if isPrefix {
			for isPrefix && err == nil {
				_, isPrefix, err = br.ReadLine()
			}
			continue
		}
		if len(line) == 0 {
			continue
		}

		var env piEnvelope
		if jsonErr := json.Unmarshal(line, &env); jsonErr != nil {
			continue
		}

		switch env.Type {
		case "session":
			var evt piSessionEvent
			if err := json.Unmarshal(line, &evt); err != nil {
				continue
			}
			if sessionID == "" && evt.ID != "" {
				sessionID = evt.ID
			}

		case "message_start":
			var evt piMessageEvent
			if err := json.Unmarshal(line, &evt); err != nil {
				continue
			}
			if evt.Message.Role == "assistant" {
				emitInit(evt.Message.Model)
			}

		case "message_update":
			var evt piMessageUpdateEvent
			if err := json.Unmarshal(line, &evt); err != nil {
				continue
			}
			switch evt.AssistantMessageEvent.Type {
			case "text_delta":
				onEvent(TextEvent{Text: evt.AssistantMessageEvent.Delta})
			case "thinking_delta":
				onEvent(ThinkingEvent{Text: evt.AssistantMessageEvent.Delta})
			}

		case "message_end":
			var evt piMessageEvent
			if err := json.Unmarshal(line, &evt); err != nil {
				continue
			}
			accumulateAssistant(evt.Message)

		case "tool_execution_end":
			var evt piToolExecutionEndEvent
			if err := json.Unmarshal(line, &evt); err != nil {
				continue
			}
			onEvent(ToolUseEvent{Name: evt.ToolName, Summary: piResultSummary(evt.Result)})

		case "agent_end":
			var evt piAgentEndEvent
			if err := json.Unmarshal(line, &evt); err != nil {
				continue
			}
			// agent_end fires when one low-level run completes and may be
			// followed by retry/compaction. Only emit ResultEvent when this
			// is not a retry checkpoint (willRetry=false).
			if evt.WillRetry {
				if msg, ok := piLastAssistant(evt.Messages); ok {
					lastStopReason = msg.StopReason
					if msg.ErrorMessage != "" {
						lastErrorMsg = redactSummary(msg.ErrorMessage)
					}
				}
				continue
			}
			sawAgentEnd = true
			if msg, ok := piLastAssistant(evt.Messages); ok {
				lastStopReason = msg.StopReason
				if msg.ErrorMessage != "" {
					lastErrorMsg = redactSummary(msg.ErrorMessage)
				}
				if piIsErrorStop(msg.StopReason) {
					sawError = true
				}
			}
			isErr := sawError || piIsErrorStop(lastStopReason) || numTurns == 0
			onEvent(ResultEvent{
				NumTurns:                 numTurns,
				TotalCostUSD:             totalCostUSD,
				IsError:                  isErr,
				ErrorMessage:             lastErrorMsg,
				Subtype:                  lastStopReason,
				InputTokens:              totalInput,
				OutputTokens:             totalOutput,
				ReasoningTokens:          totalReasoning,
				CacheCreationInputTokens: totalCacheWrite,
				CacheReadInputTokens:     totalCacheRead,
			})

		case "agent_start", "turn_start", "turn_end", "tool_execution_start",
			"tool_execution_update", "agent_settled", "queue_update":
			// Lifecycle / intermediate events — no AgentEvent mapping.

		default:
			// Unknown event types are silently skipped. Monitor for
			// schema drift after pi releases.
		}
	}

	// If no agent_end was seen (truncated stream), synthesize a ResultEvent
	// from accumulated message_end data — same fallback as OpenCode.
	if !sawAgentEnd {
		isError := sawError || numTurns == 0 || piIsErrorStop(lastStopReason)
		onEvent(ResultEvent{
			NumTurns:                 numTurns,
			TotalCostUSD:             totalCostUSD,
			IsError:                  isError,
			ErrorMessage:             lastErrorMsg,
			Subtype:                  lastStopReason,
			InputTokens:              totalInput,
			OutputTokens:             totalOutput,
			ReasoningTokens:          totalReasoning,
			CacheCreationInputTokens: totalCacheWrite,
			CacheReadInputTokens:     totalCacheRead,
		})
	}

	return sessionID, nil
}
