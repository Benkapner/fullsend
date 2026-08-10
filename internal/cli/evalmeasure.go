package cli

import (
	"errors"
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/fullsend-ai/fullsend/internal/evalmeasure"
	"github.com/fullsend-ai/fullsend/internal/ui"
)

func newEvalMeasureCmd() *cobra.Command {
	var (
		telemetryPath string
		registryPath  string
		outDir        string
	)

	cmd := &cobra.Command{
		Use:   "eval-measure",
		Short: "Score agent run traces with eval measurements",
		Long: `Parse run-telemetry.jsonl, score with an agents measurement manifest,
write eval-measurements.jsonl, and best-effort export scores to MLflow
Assessments (Quality / per-trace Assessments panel).

MLflow export uses MLFLOW_TRACKING_URI (or derives it from
OTEL_EXPORTER_OTLP_TRACES_ENDPOINT) plus MLFLOW_TRACKING_USERNAME/
MLFLOW_TRACKING_PASSWORD. The OTEL ingest Bearer token is not sufficient
for the Assessments API.

Exit 0 when scores fail — measurements are data, not gates. Non-zero only
on hard IO/parse errors. Export failures print a warning and exit 0.

The --registry path is required (manifests live in fullsend-ai/agents,
e.g. eval/measurements/<agent>.yaml).`,
		RunE: func(cmd *cobra.Command, args []string) error {
			printer := ui.New(os.Stdout)
			printer.Header("Eval Measure")

			results, err := evalmeasure.MeasureAndExport(cmd.Context(), telemetryPath, registryPath, outDir)
			if err != nil {
				if isExportWarnErr(err) {
					printer.StepWarn(fmt.Sprintf("Export failed (local measurements kept): %v", err))
					printResults(printer, results)
					return nil
				}
				return err
			}
			printResults(printer, results)
			return nil
		},
	}

	cmd.Flags().StringVar(&telemetryPath, "telemetry", "", "path to run-telemetry.jsonl (required)")
	cmd.Flags().StringVar(&registryPath, "registry", "", "path to agents measurement manifest YAML (required)")
	cmd.Flags().StringVar(&outDir, "out-dir", "", "directory for eval-measurements.jsonl (default: telemetry directory)")
	_ = cmd.MarkFlagRequired("telemetry")
	_ = cmd.MarkFlagRequired("registry")
	return cmd
}

func isExportWarnErr(err error) bool {
	var exportErr *evalmeasure.ExportError
	return errors.As(err, &exportErr)
}

func printResults(printer *ui.Printer, results []evalmeasure.EvaluationResult) {
	if len(results) == 0 {
		printer.StepDone("No new measurements (already scored or no matching traces)")
		return
	}
	for _, r := range results {
		line := fmt.Sprintf("%s %s=%.2f (%s) %s", r.Version, r.Name, r.Value, r.Label, r.Explanation)
		if r.Label == "pass" {
			printer.StepDone(line)
		} else {
			printer.StepWarn(line)
		}
	}
	printer.StepDone(fmt.Sprintf("Wrote %d measurement(s)", len(results)))
}
