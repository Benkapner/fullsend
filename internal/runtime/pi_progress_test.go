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

	// Expected: InitEvent, TextEvent, ToolUseEvent, TokensEvent, ResultEvent
	require.GreaterOrEqual(t, len(events), 5)

	// Find specific event types.
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

	// Pi emits an InitEvent from the session header (unlike OpenCode).
	require.Len(t, inits, 1)
	assert.Equal(t, "claude-sonnet-4-20250514", inits[0].Model)
	assert.Equal(t, "0.84.2", inits[0].Version)

	require.Len(t, texts, 1)
	assert.Equal(t, "I'll list the files for you.", texts[0].Text)

	require.Len(t, tools, 1)
	assert.Equal(t, "bash", tools[0].Name)
	assert.Equal(t, "$ ls", tools[0].Summary)

	require.Len(t, tokens, 1)
	assert.Equal(t, 100, tokens[0].InputTokens)
	assert.Equal(t, 50, tokens[0].OutputTokens)
	assert.Equal(t, 80, tokens[0].CacheRead)
	assert.Equal(t, 20, tokens[0].CacheWrite)

	require.Len(t, results, 1)
	assert.Equal(t, 1, results[0].NumTurns)
	assert.InDelta(t, 0.015, results[0].TotalCostUSD, 0.001)
	assert.False(t, results[0].IsError)
	assert.Equal(t, "end_turn", results[0].Subtype)
	assert.Equal(t, 100, results[0].InputTokens)
	assert.Equal(t, 50, results[0].OutputTokens)

	// ResultEvent must be the last event emitted.
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
	assert.Equal(t, "APIError", errEvents[0].ErrorType)
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

	// Reasoning tokens must be captured in per-message TokensEvent.
	require.Len(t, tokens, 1)
	assert.Equal(t, 50, tokens[0].ReasoningTokens, "reasoning tokens should be captured per-message")
	assert.Equal(t, 200, tokens[0].InputTokens)
	assert.Equal(t, 100, tokens[0].OutputTokens)

	// Reasoning tokens must appear in the ResultEvent from agent_end.
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
	// First message.
	assert.Equal(t, 100, tokens[0].InputTokens)
	assert.Equal(t, 30, tokens[0].OutputTokens)
	// Second message.
	assert.Equal(t, 200, tokens[1].InputTokens)
	assert.Equal(t, 70, tokens[1].OutputTokens)

	// ResultEvent should have cumulative totals from agent_end.
	require.Len(t, results, 1)
	assert.Equal(t, 2, results[0].NumTurns)
	assert.InDelta(t, 0.03, results[0].TotalCostUSD, 0.001)
	assert.Equal(t, 300, results[0].InputTokens)             // cumulative
	assert.Equal(t, 100, results[0].OutputTokens)            // cumulative
	assert.Equal(t, 240, results[0].CacheReadInputTokens)    // cumulative
	assert.Equal(t, 35, results[0].CacheCreationInputTokens) // cumulative
	assert.False(t, results[0].IsError)
}

func TestParsePiStream_Malformed(t *testing.T) {
	t.Parallel()

	events, sessionID := collectPiEvents(t, "malformed.ndjson")

	assert.Equal(t, "ses_pi_mal", sessionID)

	// Should get InitEvent + TextEvent from the valid lines + TokensEvent + ResultEvent.
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

	// Should get only a synthesized ResultEvent with IsError=true (zero messages).
	require.Len(t, events, 1)
	result, ok := events[0].(ResultEvent)
	require.True(t, ok)
	assert.True(t, result.IsError)
	assert.Equal(t, 0, result.NumTurns)
}

func TestParsePiStream_Truncated(t *testing.T) {
	t.Parallel()

	// Truncated stream: has message_end but no agent_end.
	// Parser should synthesize a ResultEvent from accumulated data.
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

	// Use inline ndjson to test sessionID from first event.
	input := `{"type":"session","session_id":"ses_first","model":"claude-sonnet-4-20250514","version":"0.84.2"}
{"type":"text","session_id":"ses_first","text":"hello"}
{"type":"message_end","session_id":"ses_first","usage":{"input_tokens":10,"output_tokens":5,"reasoning_tokens":0,"cache_read":0,"cache_write":0},"cost":0.01}
{"type":"agent_end","session_id":"ses_first","stop_reason":"end_turn","usage":{"input_tokens":10,"output_tokens":5,"reasoning_tokens":0,"cache_read":0,"cache_write":0},"cost":0.01}
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

	valid := `{"type":"text","session_id":"ses_x","text":"hi"}` + "\n"
	r := io.MultiReader(
		strings.NewReader(valid),
		iotest.ErrReader(errors.New("pipe broken")),
	)
	var events []AgentEvent
	sid, err := parsePiStream(r, func(e AgentEvent) { events = append(events, e) })
	require.Error(t, err)
	assert.Equal(t, "ses_x", sid)
	assert.Contains(t, err.Error(), "pipe broken")
}

func TestParsePiStream_SecretRedaction(t *testing.T) {
	t.Parallel()

	// Build fake tokens at runtime to avoid tripping gitleaks.
	ghToken := "ghp_" + strings.Repeat("x", 40)
	skToken := "sk-proj-" + strings.Repeat("y", 40)

	completedLine := fmt.Sprintf(
		`{"type":"tool_result","session_id":"ses_sec","tool":"bash","status":"completed","title":"$ curl -H \"Authorization: Bearer %s\""}`,
		ghToken,
	)
	errorLine := fmt.Sprintf(
		`{"type":"tool_result","session_id":"ses_sec","tool":"bash","status":"error","error":"request failed: token %s is expired"}`,
		skToken,
	)
	messageEnd := `{"type":"message_end","session_id":"ses_sec","usage":{"input_tokens":10,"output_tokens":5,"reasoning_tokens":0,"cache_read":0,"cache_write":0},"cost":0.01}`
	agentEnd := `{"type":"agent_end","session_id":"ses_sec","stop_reason":"end_turn","usage":{"input_tokens":10,"output_tokens":5,"reasoning_tokens":0,"cache_read":0,"cache_write":0},"cost":0.01}`

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

	// Completed tool: GitHub token in title must be redacted.
	assert.NotContains(t, tools[0].Summary, ghToken,
		"GitHub token should be redacted from completed tool summary")

	// Error tool: sk-proj token in error must be redacted.
	assert.NotContains(t, tools[1].Summary, skToken,
		"API key should be redacted from error tool summary")
}

func TestParsePiStream_ErrorStopReason(t *testing.T) {
	t.Parallel()

	// agent_end with stop_reason "error" must set IsError=true even
	// without a preceding error event — this is critical because
	// --mode json exits 0 on model error.
	input := `{"type":"session","session_id":"ses_errsr","model":"claude-sonnet-4-20250514","version":"0.84.2"}
{"type":"message_end","session_id":"ses_errsr","usage":{"input_tokens":10,"output_tokens":5,"reasoning_tokens":0,"cache_read":0,"cache_write":0},"cost":0.01}
{"type":"agent_end","session_id":"ses_errsr","stop_reason":"error","usage":{"input_tokens":10,"output_tokens":5,"reasoning_tokens":0,"cache_read":0,"cache_write":0},"cost":0.01}
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
	assert.True(t, results[0].IsError, "stop_reason=error must set IsError=true")
	assert.Equal(t, "error", results[0].Subtype)
}

func TestParsePiStream_AbortedStopReason(t *testing.T) {
	t.Parallel()

	// agent_end with stop_reason "aborted" must also set IsError=true.
	input := `{"type":"session","session_id":"ses_abort","model":"claude-sonnet-4-20250514","version":"0.84.2"}
{"type":"message_end","session_id":"ses_abort","usage":{"input_tokens":10,"output_tokens":5,"reasoning_tokens":0,"cache_read":0,"cache_write":0},"cost":0.01}
{"type":"agent_end","session_id":"ses_abort","stop_reason":"aborted","usage":{"input_tokens":10,"output_tokens":5,"reasoning_tokens":0,"cache_read":0,"cache_write":0},"cost":0.01}
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
	assert.True(t, results[0].IsError, "stop_reason=aborted must set IsError=true")
	assert.Equal(t, "aborted", results[0].Subtype)
}

func TestParsePiStream_OversizedLineSkipped(t *testing.T) {
	t.Parallel()

	huge := strings.Repeat("x", 1024*1024+100) + "\n"
	valid := `{"type":"text","session_id":"ses_big","text":"after"}` + "\n"
	messageEnd := `{"type":"message_end","session_id":"ses_big","usage":{"input_tokens":1,"output_tokens":1,"reasoning_tokens":0,"cache_read":0,"cache_write":0},"cost":0.001}` + "\n"
	agentEnd := `{"type":"agent_end","session_id":"ses_big","stop_reason":"end_turn","usage":{"input_tokens":1,"output_tokens":1,"reasoning_tokens":0,"cache_read":0,"cache_write":0},"cost":0.001}` + "\n"
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

func TestParsePiStream_ToolCallAbsorbed(t *testing.T) {
	t.Parallel()

	// tool_call events should be silently absorbed (intermediate).
	// Only tool_result emits a ToolUseEvent.
	input := `{"type":"session","session_id":"ses_tc","model":"claude-sonnet-4-20250514","version":"0.84.2"}
{"type":"tool_call","session_id":"ses_tc","tool":"bash","input":{"command":"echo hello"}}
{"type":"tool_result","session_id":"ses_tc","tool":"bash","status":"completed","title":"$ echo hello"}
{"type":"message_end","session_id":"ses_tc","usage":{"input_tokens":10,"output_tokens":5,"reasoning_tokens":0,"cache_read":0,"cache_write":0},"cost":0.01}
{"type":"agent_end","session_id":"ses_tc","stop_reason":"end_turn","usage":{"input_tokens":10,"output_tokens":5,"reasoning_tokens":0,"cache_read":0,"cache_write":0},"cost":0.01}
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

	require.Len(t, tools, 1, "only tool_result should emit ToolUseEvent")
	assert.Equal(t, "$ echo hello", tools[0].Summary)
}
