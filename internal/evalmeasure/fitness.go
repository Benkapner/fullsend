package evalmeasure

import (
	"strings"
)

const (
	ScorerFitness = "trace_fitness"
	LabelPass     = "pass"
	LabelFail     = "fail"
	LabelSkip     = "skip"

	// UnknownSentinel is the CLI fallback for missing work-item / agent
	// identity (resolveWorkItemID). Scorers must treat it as absent.
	UnknownSentinel = "unknown"
)

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
	if hasRun {
		if skipped, ok := run.AttrBool("fullsend.prescript.skipped"); ok && skipped {
			spanID := run.SpanID
			workItem, _ := run.AttrString("fullsend.work_item_id")
			reason := "pre-script skipped run; excluded from trace_fitness"
			if r, ok := run.AttrString("fullsend.prescript.skip_reason"); ok && r != "" {
				reason = reason + ": " + r
			}
			return EvaluationResult{
				Name:        evalName,
				Label:       LabelSkip,
				Explanation: reason,
				TraceID:     tr.TraceID,
				SpanID:      spanID,
				WorkItemID:  workItem,
				Agent:       tr.AgentName(),
				Version:     version,
			}
		}
	}
	agents := tr.SpansByName("agent")
	_, hasSandbox := tr.SpanByName("sandbox_create")
	costOK, costMissing := costToolsTurnsDetail(run, agents)

	type check struct {
		id   string
		pass bool
	}
	checks := []check{
		{"span_tree", hasRun && hasSandbox && len(agents) >= 1},
		{"identity", identityOK(run, agents)},
		// UnknownSentinel is the CLI fallback when no issue- or PR-shaped env is set.
		// Review jobs that have PR_NUMBER / GITHUB_PR_URL should not hit this
		// after #5622; a remaining unknown is a real fitness fail.
		{"work_item", workItemOK(run)},
		{"operation", attrNonEmpty(run, "gen_ai.operation.name")},
		{"model", modelOK(run, agents)},
		{"usage", usageOK(run, agents)},
		{"cost_tools_turns", costOK},
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
			if c.id == "cost_tools_turns" && len(costMissing) > 0 {
				details = append(details, c.id+"=fail["+strings.Join(costMissing, ",")+"]")
			} else {
				details = append(details, c.id+"=fail")
			}
		}
	}
	total := len(checks)
	value := float64(passed) / float64(total)
	label := LabelFail
	if value == 1.0 {
		label = LabelPass
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
	if !okFA || fa == "" || fa == UnknownSentinel {
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
	return ok && v != "" && v != UnknownSentinel
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

func costToolsTurnsDetail(run Span, agents []Span) (bool, []string) {
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
	var missing []string
	if !hasCost {
		missing = append(missing, "cost")
	}
	if !hasTools {
		missing = append(missing, "tool_calls")
	}
	if iters, ok := run.AttrInt("fullsend.iterations"); ok && iters > 0 {
		if _, ok := run.AttrInt("fullsend.num_turns"); !ok {
			missing = append(missing, "num_turns")
		}
	}
	return len(missing) == 0, missing
}

func hasExit(run Span) bool {
	// Fitness: attribute present. Do not treat exit_code==0 as success —
	// after #5944, OTLP Status / transcript_error carry outcome.
	_, ok := run.AttrInt("exit_code")
	return ok
}
