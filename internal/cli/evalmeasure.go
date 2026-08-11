package cli

import (
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
and write eval-measurements.jsonl beside the telemetry artifact.

Remote backends are not selected by fullsend: scores are a portable local
JSONL artifact. When portable OTLP score export lands, it will reuse the
same OTEL_EXPORTER_OTLP_* configuration as agent traces (ADR 0050 / 0087).

Exit 0 when scores fail — measurements are data, not gates. Non-zero only
on hard IO/parse errors.

The --registry path is required (manifests live in fullsend-ai/agents,
e.g. eval/measurements/<agent>.yaml).`,
		RunE: func(cmd *cobra.Command, args []string) error {
			printer := ui.New(os.Stdout)
			printer.Header("Eval Measure")

			results, err := evalmeasure.MeasureAndExport(cmd.Context(), telemetryPath, registryPath, outDir)
			if err != nil {
				return err
			}
			printMeasurementResults(printer, results)
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

func printMeasurementResults(printer *ui.Printer, results []evalmeasure.EvaluationResult) {
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
