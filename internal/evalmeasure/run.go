package evalmeasure

import (
	"context"
	"fmt"
	"path/filepath"
)

// MeasureFile parses telemetry, scores with the registry, writes local JSONL,
// and best-effort exports MLflow Assessments. Idempotent per ledger.
func MeasureFile(telemetryPath, registryPath, outDir string) ([]EvaluationResult, error) {
	return MeasureAndExport(context.Background(), telemetryPath, registryPath, outDir)
}

// MeasureAndExport is MeasureFile with an explicit context.
func MeasureAndExport(ctx context.Context, telemetryPath, registryPath, outDir string) ([]EvaluationResult, error) {
	if outDir == "" {
		outDir = filepath.Dir(telemetryPath)
	}
	reg, err := LoadRegistry(registryPath)
	if err != nil {
		return nil, fmt.Errorf("load registry: %w", err)
	}
	traces, err := ParseTelemetryFile(telemetryPath)
	if err != nil {
		return nil, fmt.Errorf("parse telemetry: %w", err)
	}

	ledgerPath := filepath.Join(outDir, LedgerFile)
	measPath := filepath.Join(outDir, MeasurementsFile)
	var all []EvaluationResult

	for _, tr := range traces {
		results := ScoreTrace(tr, reg)
		for _, r := range results {
			done, err := AlreadyScored(ledgerPath, r.TraceID, r.Name, r.Version)
			if err != nil {
				return all, fmt.Errorf("check ledger: %w", err)
			}
			if done {
				continue
			}
			all = append(all, r)
			if err := RecordScored(ledgerPath, r.TraceID, r.Name, r.Version); err != nil {
				return all, fmt.Errorf("record scored: %w", err)
			}
			if err := AppendMeasurements(measPath, []EvaluationResult{r}); err != nil {
				return all, fmt.Errorf("append measurements: %w", err)
			}
		}
	}

	if err := ExportMLflowAssessments(ctx, all); err != nil {
		return all, fmt.Errorf("export mlflow: %w", err)
	}
	return all, nil
}
