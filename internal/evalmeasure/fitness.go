package evalmeasure

import (
	"strings"
)

const ScorerFitness = "trace_fitness"

// ScoreFitness implements EM-001 Trace Fitness against a single trace.
func ScoreFitness(tr Trace) EvaluationResult {
	return ScoreFitnessNamed(tr, ScorerFitness, "em-001@1")
}

// ScoreFitnessNamed is ScoreFitness with explicit evaluation name/version.
func ScoreFitnessNamed(tr Trace, evalName, version string) EvaluationResult {
	if evalName == "" {
		evalName = ScorerFitness
	}
	if version == "" {
		version = "em-001@1"
	}

	run, hasRun := tr.SpanByName("run")
	agents := tr.SpansByName("agent")
	_, hasSandbox := tr.SpanByName("sandbox_create")

	type check struct {
		id   string
		pass bool
	}
	checks := []check{
		{"span_tree", hasRun && hasSandbox && len(agents) >= 1},
		{"identity", identityOK(run, agents)},
		// "unknown" is the CLI sentinel when no ISSUE_*/GITHUB_ISSUE_URL is set
		// (common for review, which wires PR_NUMBER / GITHUB_PR_URL instead).
		{"work_item", workItemOK(run)},
		{"operation", attrNonEmpty(run, "gen_ai.operation.name")},
		{"model", modelOK(run, agents)},
		{"usage", usageOK(run, agents)},
		{"cost_tools_turns", costToolsTurnsOK(run, agents)},
		{"exit", hasExit(run)},
	}

	passed := 0
	var failed []string
	var details []string
	for _, c := range checks {
		if c.pass {
			passed++
			details = append(details, c.id+"=pass")
		} else {
			failed = append(failed, c.id)
			details = append(details, c.id+"=fail")
		}
	}
	total := len(checks)
	value := float64(passed) / float64(total)
	label := "fail"
	if value == 1.0 {
		label = "pass"
	}
	expl := strings.Join(details, ", ")
	if len(failed) > 0 {
		expl = "missing: " + strings.Join(failed, ", ") + "; " + expl
	}

	spanID := ""
	workItem := ""
	agent := tr.AgentName()
	if hasRun {
		spanID = run.SpanID
		workItem, _ = run.AttrString("fullsend.work_item_id")
	}

	return EvaluationResult{
		Name:        evalName,
		Label:       label,
		Explanation: expl,
		TraceID:     tr.TraceID,
		SpanID:      spanID,
		WorkItemID:  workItem,
		Agent:       agent,
		Version:     version,
		Value:       value,
	}
}

func identityOK(run Span, agents []Span) bool {
	fa, okFA := run.AttrString("fullsend.agent")
	if !okFA || fa == "" || fa == "unknown" {
		return false
	}
	if ga, ok := run.AttrString("gen_ai.agent.name"); ok && ga == fa {
		return true
	}
	for _, a := range agents {
		if ga, ok := a.AttrString("gen_ai.agent.name"); ok && ga == fa {
			return true
		}
	}
	return false
}

func workItemOK(run Span) bool {
	v, ok := run.AttrString("fullsend.work_item_id")
	return ok && v != "" && v != "unknown"
}

func attrNonEmpty(s Span, key string) bool {
	v, ok := s.AttrString(key)
	return ok && v != ""
}

func modelOK(run Span, agents []Span) bool {
	hasModel := false
	if _, ok := run.AttrString("gen_ai.request.model"); ok {
		hasModel = true
	}
	if !hasModel {
		for _, a := range agents {
			if _, ok := a.AttrString("gen_ai.request.model"); ok {
				hasModel = true
				break
			}
		}
	}
	hasSystem := false
	for _, a := range agents {
		if _, ok := a.AttrString("gen_ai.system"); ok {
			hasSystem = true
			break
		}
	}
	return hasModel && hasSystem
}

func usageOK(run Span, agents []Span) bool {
	if _, ok := run.AttrInt("gen_ai.usage.input_tokens"); ok {
		if _, ok := run.AttrInt("gen_ai.usage.output_tokens"); ok {
			return true
		}
	}
	for _, a := range agents {
		_, inOK := a.AttrInt("gen_ai.usage.input_tokens")
		_, outOK := a.AttrInt("gen_ai.usage.output_tokens")
		if inOK && outOK {
			return true
		}
	}
	return false
}

func costToolsTurnsOK(run Span, agents []Span) bool {
	hasCost := false
	hasTools := false
	if _, ok := run.AttrFloat("fullsend.cost_usd"); ok {
		hasCost = true
	}
	if _, ok := run.AttrInt("fullsend.tool_calls"); ok {
		hasTools = true
	}
	for _, a := range agents {
		if !hasCost {
			if _, ok := a.AttrFloat("fullsend.cost_usd"); ok {
				hasCost = true
			}
		}
		if !hasTools {
			if _, ok := a.AttrInt("fullsend.tool_calls"); ok {
				hasTools = true
			}
		}
	}
	if !hasCost || !hasTools {
		return false
	}
	if iters, ok := run.AttrInt("fullsend.iterations"); ok && iters > 0 {
		if _, ok := run.AttrInt("fullsend.num_turns"); !ok {
			return false
		}
	}
	return true
}

func hasExit(run Span) bool {
	// Fitness: attribute present. Do not treat exit_code==0 as success —
	// after #5944, OTLP Status / transcript_error carry outcome.
	_, ok := run.AttrInt("exit_code")
	return ok
}
