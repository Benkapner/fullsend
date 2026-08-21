package runtime

import (
	"bufio"
	"encoding/json"
	"io"
)

// Pi --mode json event types and field paths verified against
// earendil-works/pi 0.84.2 --print --mode json output.
// Re-verify after pi releases change the wire format (fast cadence:
// ~weekly minors; 0.84.0 changed message_update shape).

// piEnvelope is the common shape of every NDJSON line from pi --mode json.
type piEnvelope struct {
	Type      string `json:"type"`
	SessionID string `json:"session_id"`
}

// session event — emitted first, carries runtime metadata.
type piSessionEvent struct {
	SessionID string `json:"session_id"`
	Model     string `json:"model"`
	Version   string `json:"version"`
}

// text event payload.
type piTextEvent struct {
	Text string `json:"text"`
}

// thinking event payload.
type piThinkingEvent struct {
	Text string `json:"text"`
}

// tool_result event — emitted when a tool invocation completes.
type piToolResultEvent struct {
	Tool   string `json:"tool"`
	Status string `json:"status"` // "completed" or "error"
	Title  string `json:"title,omitempty"`
	Error  string `json:"error,omitempty"`
}

// message_end event — per-message token usage and cost.
type piMessageEndEvent struct {
	Usage piUsage `json:"usage"`
	Cost  float64 `json:"cost"`
}

type piUsage struct {
	InputTokens     int `json:"input_tokens"`
	OutputTokens    int `json:"output_tokens"`
	ReasoningTokens int `json:"reasoning_tokens"`
	CacheRead       int `json:"cache_read"`
	CacheWrite      int `json:"cache_write"`
}

// agent_end event — terminal sentinel with stop reason and cumulative usage.
type piAgentEndEvent struct {
	StopReason string  `json:"stop_reason"`
	Usage      piUsage `json:"usage"`
	Cost       float64 `json:"cost"`
}

// error event payload.
type piErrorEvent struct {
	Name    string `json:"name"`
	Message string `json:"message"`
}

// parsePiStream reads NDJSON from pi's --mode json output and emits
// normalized AgentEvent values via the onEvent callback. It returns the
// sessionID captured from the session header.
//
// Pi's wire format differs from both Claude and OpenCode:
//   - An explicit session header carries model/version metadata → InitEvent.
//   - message_end events carry per-message token usage → TokensEvent.
//   - An explicit agent_end terminal event carries stop_reason → ResultEvent.
//   - Tool names are lowercase (bash, read, write, edit, glob, grep) — the
//     hook adapter translates to Claude-name vocabulary (#608).
//   - --mode json exits 0 on model error (stopReason: error/aborted only
//     maps to exit 1 in text mode) — ParseTranscriptFile must detect errors
//     from the stream, not the exit code.
//
// The parser accumulates message_end stats for ResultEvent synthesis but
// prefers the explicit agent_end event when present (unlike OpenCode, which
// has no terminal sentinel and must synthesize at EOF).
func parsePiStream(r io.Reader, onEvent func(AgentEvent)) (sessionID string, err error) {
	br := bufio.NewReaderSize(r, streamBufSize)

	var (
		// Accumulated state for ResultEvent synthesis (fallback when
		// agent_end is missing, e.g. truncated stream).
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
	)

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

		// Capture sessionID from the first event that has one.
		if sessionID == "" && env.SessionID != "" {
			sessionID = env.SessionID
		}

		switch env.Type {
		case "session":
			var evt piSessionEvent
			if err := json.Unmarshal(line, &evt); err != nil {
				continue
			}
			onEvent(InitEvent{
				Model:   evt.Model,
				Version: evt.Version,
			})

		case "text":
			var evt piTextEvent
			if err := json.Unmarshal(line, &evt); err != nil {
				continue
			}
			onEvent(TextEvent{Text: evt.Text})

		case "thinking":
			var evt piThinkingEvent
			if err := json.Unmarshal(line, &evt); err != nil {
				continue
			}
			onEvent(ThinkingEvent{Text: evt.Text})

		case "tool_result":
			var evt piToolResultEvent
			if err := json.Unmarshal(line, &evt); err != nil {
				continue
			}
			summary := redactSummary(evt.Title)
			if evt.Status == "error" {
				summary = redactSummary(evt.Error)
			}
			onEvent(ToolUseEvent{Name: evt.Tool, Summary: summary})

		case "message_end":
			var evt piMessageEndEvent
			if err := json.Unmarshal(line, &evt); err != nil {
				continue
			}
			numTurns++
			totalCostUSD += evt.Cost
			totalInput += evt.Usage.InputTokens
			totalOutput += evt.Usage.OutputTokens
			totalReasoning += evt.Usage.ReasoningTokens
			totalCacheRead += evt.Usage.CacheRead
			totalCacheWrite += evt.Usage.CacheWrite

			onEvent(TokensEvent{
				InputTokens:     evt.Usage.InputTokens,
				OutputTokens:    evt.Usage.OutputTokens,
				ReasoningTokens: evt.Usage.ReasoningTokens,
				CacheRead:       evt.Usage.CacheRead,
				CacheWrite:      evt.Usage.CacheWrite,
			})

		case "agent_end":
			var evt piAgentEndEvent
			if err := json.Unmarshal(line, &evt); err != nil {
				continue
			}
			sawAgentEnd = true
			lastStopReason = evt.StopReason

			// Detect error from stop_reason: --mode json exits 0 on
			// model error, so the stream is the authoritative error
			// signal (not the process exit code).
			isErr := sawError || evt.StopReason == "error" || evt.StopReason == "aborted"

			onEvent(ResultEvent{
				NumTurns:                 numTurns,
				TotalCostUSD:             evt.Cost,
				IsError:                  isErr,
				ErrorMessage:             lastErrorMsg,
				Subtype:                  evt.StopReason,
				InputTokens:              evt.Usage.InputTokens,
				OutputTokens:             evt.Usage.OutputTokens,
				ReasoningTokens:          evt.Usage.ReasoningTokens,
				CacheCreationInputTokens: evt.Usage.CacheWrite,
				CacheReadInputTokens:     evt.Usage.CacheRead,
			})

		case "error":
			var evt piErrorEvent
			if err := json.Unmarshal(line, &evt); err != nil {
				continue
			}
			sawError = true
			msg := redactSummary(evt.Message)
			lastErrorMsg = msg
			onEvent(ErrorEvent{
				ErrorType: evt.Name,
				Message:   msg,
			})

		case "tool_call":
			// Silently absorbed — tool_call is intermediate; we emit
			// ToolUseEvent from tool_result when the call completes.

		default:
			// Unknown event types are silently skipped. Monitor for
			// schema drift after pi releases.
		}
	}

	// If no agent_end was seen (truncated stream), synthesize a ResultEvent
	// from accumulated message_end data — same fallback as OpenCode.
	if !sawAgentEnd {
		isError := sawError || numTurns == 0
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
