package runtime

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/iotest"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func loadPiFixture(t *testing.T, name string) *os.File {
	t.Helper()
	path := filepath.Join("testdata", "pi", name)
	f, err := os.Open(path)
	require.NoError(t, err)
	t.Cleanup(func() { f.Close() })
	return f
}

func collectPiEvents(t *testing.T, name string) ([]AgentEvent, string) {
	t.Helper()
	f := loadPiFixture(t, name)
	var events []AgentEvent
	sessionID, err := parsePiStream(f, func(evt AgentEvent) {
		events = append(events, evt)
	})
	require.NoError(t, err)
	return events, sessionID
}

func TestParsePiStream_BasicRun(t *testing.T) {
	t.Parallel()

	events, sessionID := collectPiEvents(t, "basic_run.ndjson")

	assert.Equal(t, "ses_pi_abc123", sessionID)

	var inits []InitEvent
	var texts []TextEvent
	var tools []ToolUseEvent
	var tokens []TokensEvent
	var results []ResultEvent
	for _, evt := range events {
		switch e := evt.(type) {
		case InitEvent:
			inits = append(inits, e)
		case TextEvent:
			texts = append(texts, e)
		case ToolUseEvent:
			tools = append(tools, e)
		case TokensEvent:
			tokens = append(tokens, e)
		case ResultEvent:
			results = append(results, e)
		}
	}

	// Model comes from the assistant message, not the session header.
	require.Len(t, inits, 1)
	assert.Equal(t, "claude-sonnet-4-20250514", inits[0].Model)
	assert.Empty(t, inits[0].Version, "CLI version is not on the --mode json wire")

	require.Len(t, texts, 1)
	assert.Equal(t, "I'll list the files for you.", texts[0].Text)

	require.Len(t, tools, 1)
	assert.Equal(t, "bash", tools[0].Name)
	assert.Equal(t, "file1.txt\nfile2.txt", tools[0].Summary)

	require.Len(t, tokens, 1)
	assert.Equal(t, 100, tokens[0].InputTokens)
	assert.Equal(t, 50, tokens[0].OutputTokens)
	assert.Equal(t, 80, tokens[0].CacheRead)
	assert.Equal(t, 20, tokens[0].CacheWrite)

	require.Len(t, results, 1)
	assert.Equal(t, 1, results[0].NumTurns)
	assert.InDelta(t, 0.015, results[0].TotalCostUSD, 0.001)
	assert.False(t, results[0].IsError)
	assert.Equal(t, "stop", results[0].Subtype)
	assert.Equal(t, 100, results[0].InputTokens)
	assert.Equal(t, 50, results[0].OutputTokens)

	_, isResult := events[len(events)-1].(ResultEvent)
	assert.True(t, isResult, "ResultEvent should be the last event")
}

func TestParsePiStream_ErrorRun(t *testing.T) {
	t.Parallel()

	events, sessionID := collectPiEvents(t, "error_run.ndjson")

	assert.Equal(t, "ses_pi_err456", sessionID)

	var errEvents []ErrorEvent
	var tools []ToolUseEvent
	var results []ResultEvent
	for _, evt := range events {
		switch e := evt.(type) {
		case ErrorEvent:
			errEvents = append(errEvents, e)
		case ToolUseEvent:
			tools = append(tools, e)
		case ResultEvent:
			results = append(results, e)
		}
	}

	require.Len(t, tools, 1)
	assert.Equal(t, "edit", tools[0].Name)
	assert.Equal(t, "file not found", tools[0].Summary)

	require.Len(t, errEvents, 1)
	assert.Equal(t, "error", errEvents[0].ErrorType)
	assert.Equal(t, "quota exhausted", errEvents[0].Message)

	require.Len(t, results, 1)
	assert.True(t, results[0].IsError)
	assert.Equal(t, "quota exhausted", results[0].ErrorMessage)
	assert.Equal(t, "error", results[0].Subtype)
	assert.Equal(t, 1, results[0].NumTurns)
	assert.Equal(t, 50, results[0].InputTokens)
	assert.Equal(t, 10, results[0].OutputTokens)
	assert.Equal(t, 40, results[0].CacheReadInputTokens)
	assert.Equal(t, 5, results[0].CacheCreationInputTokens)
}

func TestParsePiStream_Reasoning(t *testing.T) {
	t.Parallel()

	events, _ := collectPiEvents(t, "reasoning_run.ndjson")

	var thinking []ThinkingEvent
	var texts []TextEvent
	var tokens []TokensEvent
	var results []ResultEvent
	for _, evt := range events {
		switch e := evt.(type) {
		case ThinkingEvent:
			thinking = append(thinking, e)
		case TextEvent:
			texts = append(texts, e)
		case TokensEvent:
			tokens = append(tokens, e)
		case ResultEvent:
			results = append(results, e)
		}
	}

	require.Len(t, thinking, 1)
	assert.Equal(t, "Let me think about the best approach...", thinking[0].Text)

	require.Len(t, texts, 1)
	assert.Equal(t, "Here is my answer.", texts[0].Text)

	require.Len(t, tokens, 1)
	assert.Equal(t, 50, tokens[0].ReasoningTokens, "reasoning tokens should be captured per-message")
	assert.Equal(t, 200, tokens[0].InputTokens)
	assert.Equal(t, 100, tokens[0].OutputTokens)

	require.Len(t, results, 1)
	assert.Equal(t, 50, results[0].ReasoningTokens, "reasoning tokens should appear in ResultEvent")
}

func TestParsePiStream_MultiStep(t *testing.T) {
	t.Parallel()

	events, _ := collectPiEvents(t, "multi_step.ndjson")

	var tokens []TokensEvent
	var results []ResultEvent
	for _, evt := range events {
		switch e := evt.(type) {
		case TokensEvent:
			tokens = append(tokens, e)
		case ResultEvent:
			results = append(results, e)
		}
	}

	require.Len(t, tokens, 2)
	assert.Equal(t, 100, tokens[0].InputTokens)
	assert.Equal(t, 30, tokens[0].OutputTokens)
	assert.Equal(t, 200, tokens[1].InputTokens)
	assert.Equal(t, 70, tokens[1].OutputTokens)

	require.Len(t, results, 1)
	assert.Equal(t, 2, results[0].NumTurns)
	assert.InDelta(t, 0.03, results[0].TotalCostUSD, 0.001)
	assert.Equal(t, 300, results[0].InputTokens)
	assert.Equal(t, 100, results[0].OutputTokens)
	assert.Equal(t, 240, results[0].CacheReadInputTokens)
	assert.Equal(t, 35, results[0].CacheCreationInputTokens)
	assert.False(t, results[0].IsError)
}

func TestParsePiStream_Malformed(t *testing.T) {
	t.Parallel()

	events, sessionID := collectPiEvents(t, "malformed.ndjson")

	assert.Equal(t, "ses_pi_mal", sessionID)

	var texts []TextEvent
	var results []ResultEvent
	for _, evt := range events {
		switch e := evt.(type) {
		case TextEvent:
			texts = append(texts, e)
		case ResultEvent:
			results = append(results, e)
		}
	}

	require.Len(t, texts, 1)
	assert.Equal(t, "valid line", texts[0].Text)

	require.Len(t, results, 1)
	assert.Equal(t, 1, results[0].NumTurns)
	assert.False(t, results[0].IsError)
}

func TestParsePiStream_Empty(t *testing.T) {
	t.Parallel()

	events, sessionID := collectPiEvents(t, "empty.ndjson")

	assert.Empty(t, sessionID)

	require.Len(t, events, 1)
	result, ok := events[0].(ResultEvent)
	require.True(t, ok)
	assert.True(t, result.IsError)
	assert.Equal(t, 0, result.NumTurns)
}

func TestParsePiStream_Truncated(t *testing.T) {
	t.Parallel()

	events, sessionID := collectPiEvents(t, "truncated.ndjson")

	assert.Equal(t, "ses_pi_trunc", sessionID)

	var results []ResultEvent
	for _, evt := range events {
		if e, ok := evt.(ResultEvent); ok {
			results = append(results, e)
		}
	}

	require.Len(t, results, 1)
	assert.Equal(t, 1, results[0].NumTurns)
	assert.InDelta(t, 0.015, results[0].TotalCostUSD, 0.001)
	assert.False(t, results[0].IsError)
	assert.Equal(t, 100, results[0].InputTokens)
	assert.Equal(t, 50, results[0].OutputTokens)
	assert.Equal(t, 80, results[0].CacheReadInputTokens)
	assert.Equal(t, 20, results[0].CacheCreationInputTokens)
}

func TestParsePiStream_SessionID(t *testing.T) {
	t.Parallel()

	input := `{"type":"session","version":3,"id":"ses_first","timestamp":"2026-08-14T12:00:00.000Z","cwd":"/tmp"}
{"type":"message_update","usage":{"input":10,"output":5,"cacheRead":0,"cacheWrite":0,"totalTokens":15,"cost":{"total":0.01}},"assistantMessageEvent":{"type":"text_delta","contentIndex":0,"delta":"hello"}}
{"type":"message_end","message":{"role":"assistant","model":"claude-sonnet-4-20250514","usage":{"input":10,"output":5,"cacheRead":0,"cacheWrite":0,"cost":{"total":0.01}},"stopReason":"stop"}}
{"type":"agent_end","messages":[{"role":"assistant","model":"claude-sonnet-4-20250514","usage":{"input":10,"output":5,"cacheRead":0,"cacheWrite":0,"cost":{"total":0.01}},"stopReason":"stop"}],"willRetry":false}
`
	var events []AgentEvent
	sessionID, err := parsePiStream(strings.NewReader(input), func(evt AgentEvent) {
		events = append(events, evt)
	})
	require.NoError(t, err)
	assert.Equal(t, "ses_first", sessionID)
	assert.Len(t, events, 4) // InitEvent + TextEvent + TokensEvent + ResultEvent
}

func TestParsePiStream_ReadError(t *testing.T) {
	t.Parallel()

	valid := `{"type":"message_update","assistantMessageEvent":{"type":"text_delta","delta":"hi"}}` + "\n"
	r := io.MultiReader(
		strings.NewReader(valid),
		iotest.ErrReader(errors.New("pipe broken")),
	)
	var events []AgentEvent
	sid, err := parsePiStream(r, func(e AgentEvent) { events = append(events, e) })
	require.Error(t, err)
	assert.Empty(t, sid)
	assert.Contains(t, err.Error(), "pipe broken")
}

func TestParsePiStream_SecretRedaction(t *testing.T) {
	t.Parallel()

	ghToken := "ghp_" + strings.Repeat("x", 40)
	skToken := "sk-proj-" + strings.Repeat("y", 40)

	completedLine := fmt.Sprintf(
		`{"type":"tool_execution_end","toolCallId":"c1","toolName":"bash","result":"curl -H \"Authorization: Bearer %s\"","isError":false}`,
		ghToken,
	)
	errorLine := fmt.Sprintf(
		`{"type":"tool_execution_end","toolCallId":"c2","toolName":"bash","result":"request failed: token %s is expired","isError":true}`,
		skToken,
	)
	messageEnd := `{"type":"message_end","message":{"role":"assistant","model":"m","usage":{"input":10,"output":5,"cacheRead":0,"cacheWrite":0,"cost":{"total":0.01}},"stopReason":"stop"}}`
	agentEnd := `{"type":"agent_end","messages":[{"role":"assistant","stopReason":"stop"}],"willRetry":false}`

	input := completedLine + "\n" + errorLine + "\n" + messageEnd + "\n" + agentEnd + "\n"

	var events []AgentEvent
	_, err := parsePiStream(strings.NewReader(input), func(evt AgentEvent) {
		events = append(events, evt)
	})
	require.NoError(t, err)

	var tools []ToolUseEvent
	for _, evt := range events {
		if e, ok := evt.(ToolUseEvent); ok {
			tools = append(tools, e)
		}
	}

	require.Len(t, tools, 2)
	assert.NotContains(t, tools[0].Summary, ghToken,
		"GitHub token should be redacted from completed tool summary")
	assert.NotContains(t, tools[1].Summary, skToken,
		"API key should be redacted from error tool summary")
}

func TestParsePiStream_ErrorStopReason(t *testing.T) {
	t.Parallel()

	// Assistant stopReason "error" must set IsError and ErrorMessage even
	// without a distinct error event — --mode json exits 0 on model error.
	input := `{"type":"session","version":3,"id":"ses_errsr","timestamp":"2026-08-14T12:00:00.000Z","cwd":"/tmp"}
{"type":"message_end","message":{"role":"assistant","model":"claude-sonnet-4-20250514","usage":{"input":10,"output":5,"cacheRead":0,"cacheWrite":0,"cost":{"total":0.01}},"stopReason":"error","errorMessage":"model overloaded"}}
{"type":"agent_end","messages":[{"role":"assistant","model":"claude-sonnet-4-20250514","usage":{"input":10,"output":5,"cacheRead":0,"cacheWrite":0,"cost":{"total":0.01}},"stopReason":"error","errorMessage":"model overloaded"}],"willRetry":false}
`
	var events []AgentEvent
	_, err := parsePiStream(strings.NewReader(input), func(evt AgentEvent) {
		events = append(events, evt)
	})
	require.NoError(t, err)

	var results []ResultEvent
	for _, evt := range events {
		if e, ok := evt.(ResultEvent); ok {
			results = append(results, e)
		}
	}

	require.Len(t, results, 1)
	assert.True(t, results[0].IsError, "stopReason=error must set IsError=true")
	assert.Equal(t, "error", results[0].Subtype)
	assert.Equal(t, "model overloaded", results[0].ErrorMessage)
}

func TestParsePiStream_AbortedStopReason(t *testing.T) {
	t.Parallel()

	input := `{"type":"session","version":3,"id":"ses_abort","timestamp":"2026-08-14T12:00:00.000Z","cwd":"/tmp"}
{"type":"message_end","message":{"role":"assistant","model":"claude-sonnet-4-20250514","usage":{"input":10,"output":5,"cacheRead":0,"cacheWrite":0,"cost":{"total":0.01}},"stopReason":"aborted","errorMessage":"request aborted"}}
{"type":"agent_end","messages":[{"role":"assistant","model":"claude-sonnet-4-20250514","usage":{"input":10,"output":5,"cacheRead":0,"cacheWrite":0,"cost":{"total":0.01}},"stopReason":"aborted","errorMessage":"request aborted"}],"willRetry":false}
`
	var events []AgentEvent
	_, err := parsePiStream(strings.NewReader(input), func(evt AgentEvent) {
		events = append(events, evt)
	})
	require.NoError(t, err)

	var results []ResultEvent
	for _, evt := range events {
		if e, ok := evt.(ResultEvent); ok {
			results = append(results, e)
		}
	}

	require.Len(t, results, 1)
	assert.True(t, results[0].IsError, "stopReason=aborted must set IsError=true")
	assert.Equal(t, "aborted", results[0].Subtype)
	assert.Equal(t, "request aborted", results[0].ErrorMessage)
}

func TestParsePiStream_OversizedLineSkipped(t *testing.T) {
	t.Parallel()

	huge := strings.Repeat("x", 1024*1024+100) + "\n"
	valid := `{"type":"message_update","assistantMessageEvent":{"type":"text_delta","delta":"after"}}` + "\n"
	messageEnd := `{"type":"message_end","message":{"role":"assistant","model":"m","usage":{"input":1,"output":1,"cacheRead":0,"cacheWrite":0,"cost":{"total":0.001}},"stopReason":"stop"}}` + "\n"
	agentEnd := `{"type":"agent_end","messages":[{"role":"assistant","stopReason":"stop"}],"willRetry":false}` + "\n"
	r := strings.NewReader(huge + valid + messageEnd + agentEnd)

	var events []AgentEvent
	_, err := parsePiStream(r, func(e AgentEvent) { events = append(events, e) })
	require.NoError(t, err)

	var texts []TextEvent
	for _, e := range events {
		if te, ok := e.(TextEvent); ok {
			texts = append(texts, te)
		}
	}
	require.Len(t, texts, 1)
	assert.Equal(t, "after", texts[0].Text)
}

func TestParsePiStream_ToolExecutionStartAbsorbed(t *testing.T) {
	t.Parallel()

	input := `{"type":"session","version":3,"id":"ses_tc","timestamp":"2026-08-14T12:00:00.000Z","cwd":"/tmp"}
{"type":"tool_execution_start","toolCallId":"c1","toolName":"bash","args":{"command":"echo hello"}}
{"type":"tool_execution_end","toolCallId":"c1","toolName":"bash","result":"$ echo hello","isError":false}
{"type":"message_end","message":{"role":"assistant","model":"m","usage":{"input":10,"output":5,"cacheRead":0,"cacheWrite":0,"cost":{"total":0.01}},"stopReason":"stop"}}
{"type":"agent_end","messages":[{"role":"assistant","stopReason":"stop"}],"willRetry":false}
`
	var events []AgentEvent
	_, err := parsePiStream(strings.NewReader(input), func(evt AgentEvent) {
		events = append(events, evt)
	})
	require.NoError(t, err)

	var tools []ToolUseEvent
	for _, evt := range events {
		if e, ok := evt.(ToolUseEvent); ok {
			tools = append(tools, e)
		}
	}

	require.Len(t, tools, 1, "only tool_execution_end should emit ToolUseEvent")
	assert.Equal(t, "$ echo hello", tools[0].Summary)
}

func TestParsePiStream_ToolResultObjectSummary(t *testing.T) {
	t.Parallel()

	input := `{"type":"tool_execution_end","toolCallId":"c1","toolName":"bash","result":{"output":"ok from object"},"isError":false}
{"type":"tool_execution_end","toolCallId":"c2","toolName":"edit","result":{"error":"file not found"},"isError":true}
{"type":"agent_end","messages":[],"willRetry":false}
`
	var tools []ToolUseEvent
	_, err := parsePiStream(strings.NewReader(input), func(evt AgentEvent) {
		if e, ok := evt.(ToolUseEvent); ok {
			tools = append(tools, e)
		}
	})
	require.NoError(t, err)
	require.Len(t, tools, 2)
	assert.Equal(t, "ok from object", tools[0].Summary)
	assert.Equal(t, "file not found", tools[1].Summary)
}

func TestParsePiStream_AgentEndWillRetry(t *testing.T) {
	t.Parallel()

	// willRetry=true is a checkpoint, not the terminal result. A later
	// agent_end with willRetry=false should be the only ResultEvent.
	input := `{"type":"session","version":3,"id":"ses_retry","timestamp":"2026-08-14T12:00:00.000Z","cwd":"/tmp"}
{"type":"agent_end","messages":[],"willRetry":true}
{"type":"message_update","assistantMessageEvent":{"type":"text_delta","delta":"recovered"}}
{"type":"message_end","message":{"role":"assistant","model":"claude-sonnet-4-20250514","usage":{"input":10,"output":5,"cacheRead":0,"cacheWrite":0,"cost":{"total":0.01}},"stopReason":"stop"}}
{"type":"agent_end","messages":[{"role":"assistant","model":"claude-sonnet-4-20250514","stopReason":"stop"}],"willRetry":false}
`
	var results []ResultEvent
	var texts []TextEvent
	_, err := parsePiStream(strings.NewReader(input), func(evt AgentEvent) {
		switch e := evt.(type) {
		case ResultEvent:
			results = append(results, e)
		case TextEvent:
			texts = append(texts, e)
		}
	})
	require.NoError(t, err)
	require.Len(t, texts, 1)
	require.Len(t, results, 1, "willRetry=true agent_end must not emit ResultEvent")
	assert.False(t, results[0].IsError)
	assert.Equal(t, 1, results[0].NumTurns)
	assert.Equal(t, "stop", results[0].Subtype)
}

func TestParsePiStream_InventedSchemaIsDropped(t *testing.T) {
	t.Parallel()

	// The previous fixture vocabulary (top-level text / tool_result /
	// snake_case stop_reason) is not pi's wire format. A real stream using
	// only those names must not produce TextEvent/ToolUseEvent/TokensEvent.
	input := `{"type":"session","session_id":"ses_fake","model":"claude-sonnet-4-20250514","version":"0.84.2"}
{"type":"text","session_id":"ses_fake","text":"should not appear"}
{"type":"tool_result","session_id":"ses_fake","tool":"bash","status":"completed","title":"$ ls"}
{"type":"message_end","session_id":"ses_fake","usage":{"input_tokens":10,"output_tokens":5},"cost":0.01}
{"type":"agent_end","session_id":"ses_fake","stop_reason":"end_turn"}
`
	var events []AgentEvent
	sessionID, err := parsePiStream(strings.NewReader(input), func(evt AgentEvent) {
		events = append(events, evt)
	})
	require.NoError(t, err)
	assert.Empty(t, sessionID, "session id is `id`, not session_id")

	for _, evt := range events {
		switch evt.(type) {
		case TextEvent, ToolUseEvent, TokensEvent, InitEvent:
			t.Fatalf("invented schema must not produce %T", evt)
		}
	}
	require.Len(t, events, 1)
	result, ok := events[0].(ResultEvent)
	require.True(t, ok)
	assert.True(t, result.IsError)
	assert.Equal(t, 0, result.NumTurns)
}
