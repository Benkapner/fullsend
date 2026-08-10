package evalmeasure

import (
	"fmt"
	"os"
	"strings"

	"gopkg.in/yaml.v3"
)

// Registry is an agent-specific measurement manifest (owned by agents repo).
type Registry struct {
	Agent        string            `yaml:"agent"`
	Measurements []MeasurementSpec `yaml:"measurements"`
}

// MeasurementSpec selects a framework scorer.
type MeasurementSpec struct {
	ID      string `yaml:"id"`
	Scorer  string `yaml:"scorer"`
	Name    string `yaml:"name"` // optional display override; default = Scorer
	Version int    `yaml:"version"`
}

// LoadRegistry loads a measurement manifest YAML file.
func LoadRegistry(path string) (Registry, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return Registry{}, err
	}
	var reg Registry
	if err := yaml.Unmarshal(b, &reg); err != nil {
		return Registry{}, err
	}
	if reg.Agent == "" {
		return Registry{}, fmt.Errorf("manifest %s: agent is required", path)
	}
	for i, m := range reg.Measurements {
		if m.ID == "" {
			return Registry{}, fmt.Errorf("manifest %s: measurements[%d].id is required", path, i)
		}
		if m.Scorer == "" {
			return Registry{}, fmt.Errorf("manifest %s: measurements[%d].scorer is required", path, i)
		}
		if m.Version <= 0 {
			return Registry{}, fmt.Errorf("manifest %s: measurements[%d].version must be >= 1", path, i)
		}
		if strings.ContainsAny(m.ID, "|\n") || strings.ContainsAny(m.Scorer, "|\n") || strings.ContainsAny(m.Name, "|\n") {
			return Registry{}, fmt.Errorf("manifest %s: measurements[%d].id, .scorer, and .name must not contain pipe or newline", path, i)
		}
	}
	return reg, nil
}

func (m MeasurementSpec) versionString() string {
	return fmt.Sprintf("%s@%d", m.ID, m.Version)
}

func (m MeasurementSpec) evalName() string {
	if m.Name != "" {
		return m.Name
	}
	return m.Scorer
}

// ScoreTrace runs enabled measurements for traces matching the manifest agent.
func ScoreTrace(tr Trace, reg Registry) []EvaluationResult {
	if tr.AgentName() != reg.Agent {
		return nil
	}
	var out []EvaluationResult
	for _, m := range reg.Measurements {
		switch m.Scorer {
		case ScorerFitness:
			out = append(out, ScoreFitnessNamed(tr, m.evalName(), m.versionString()))
		default:
			// Unknown scorers are skipped (forward-compatible).
		}
	}
	return out
}
