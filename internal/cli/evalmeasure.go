package cli

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	"github.com/fullsend-ai/fullsend/internal/config"
	"github.com/fullsend-ai/fullsend/internal/evalmeasure"
	"github.com/fullsend-ai/fullsend/internal/fetch"
	"github.com/fullsend-ai/fullsend/internal/forge"
	gh "github.com/fullsend-ai/fullsend/internal/forge/github"
	"github.com/fullsend-ai/fullsend/internal/harness"
	"github.com/fullsend-ai/fullsend/internal/ui"
)

func newEvalMeasureCmd() *cobra.Command {
	var (
		telemetryPath string
		registryPath  string
		outDir        string
		outputDir     string
		agent         string
		fullsendDir   string
	)

	cmd := &cobra.Command{
		Use:   "eval-measure",
		Short: "Score agent run traces with eval measurements",
		Long: `Parse run-telemetry.jsonl, score with an agents measurement manifest,
and write eval-measurements.jsonl beside the telemetry artifact.

The binary resolves the manifest (local FULLSEND_DIR override, else a
SHA-pinned fetch from fullsend-ai/agents — same pin, allowlist, hash, and
audit as harness fallback). Platform telemetry is the file at the top of
each run directory; nested iteration-N/output/ copies are ignored.

Remote backends are not selected by fullsend: scores are a portable local
JSONL artifact. When portable OTLP score export lands, it will reuse the
same OTEL_EXPORTER_OTLP_* configuration as agent traces (ADR 0050 / 0087).

Exit 0 when scores fail — measurements are data, not gates. Non-zero only
on hard IO/parse errors. Missing telemetry or manifest is a skip (exit 0).`,
		RunE: func(cmd *cobra.Command, args []string) error {
			printer := ui.New(cmd.OutOrStdout())
			printer.Header("Eval Measure")

			results, skipped, err := runEvalMeasure(cmd.Context(), printer, evalMeasureOpts{
				telemetryPath: telemetryPath,
				registryPath:  registryPath,
				outDir:        outDir,
				outputDir:     outputDir,
				agent:         agent,
				fullsendDir:   fullsendDir,
			})
			if err != nil {
				printMeasurementResults(printer, results, false)
				return err
			}
			if skipped {
				return nil
			}
			printMeasurementResults(printer, results, true)
			return nil
		},
	}

	cmd.Flags().StringVar(&telemetryPath, "telemetry", "", "path to run-telemetry.jsonl (or use --output-dir)")
	cmd.Flags().StringVar(&registryPath, "registry", "", "path to agents measurement manifest YAML (or use --agent)")
	cmd.Flags().StringVar(&outDir, "out-dir", "", "directory for eval-measurements.jsonl (default: telemetry directory)")
	cmd.Flags().StringVar(&outputDir, "output-dir", "", "CI output base or runDir; scores only top-of-runDir telemetry")
	cmd.Flags().StringVar(&agent, "agent", "", "agent name for manifest resolution")
	cmd.Flags().StringVar(&fullsendDir, "fullsend-dir", "", "path to the .fullsend directory (local manifest override + fetch cache)")
	return cmd
}

type evalMeasureOpts struct {
	telemetryPath string
	registryPath  string
	outDir        string
	outputDir     string
	agent         string
	fullsendDir   string
}

func runEvalMeasure(ctx context.Context, printer *ui.Printer, opts evalMeasureOpts) ([]evalmeasure.EvaluationResult, bool, error) {
	telemPaths, err := resolveEvalMeasureTelemetry(opts)
	if err != nil {
		return nil, false, err
	}
	if len(telemPaths) == 0 {
		printer.StepInfo("No platform run-telemetry.jsonl at the top of a run directory; skipping eval measurements")
		return nil, true, nil
	}

	registry, err := resolveEvalMeasureRegistry(ctx, printer, opts)
	if err != nil {
		return nil, false, err
	}
	if registry == "" {
		printer.StepInfo("No eval measurement manifest; skipping")
		return nil, true, nil
	}

	var all []evalmeasure.EvaluationResult
	for _, p := range telemPaths {
		results, err := evalmeasure.MeasureAndExport(ctx, p, registry, opts.outDir)
		if err != nil {
			return all, false, err
		}
		all = append(all, results...)
	}
	return all, false, nil
}

func resolveEvalMeasureTelemetry(opts evalMeasureOpts) ([]string, error) {
	if opts.telemetryPath != "" {
		return []string{opts.telemetryPath}, nil
	}
	if opts.outputDir != "" {
		return evalmeasure.FindPlatformTelemetry(opts.outputDir)
	}
	return nil, fmt.Errorf("either --telemetry or --output-dir is required")
}

func resolveEvalMeasureRegistry(ctx context.Context, printer *ui.Printer, opts evalMeasureOpts) (string, error) {
	if opts.registryPath != "" {
		return opts.registryPath, nil
	}
	if opts.agent == "" {
		return "", fmt.Errorf("either --registry or --agent is required")
	}
	agent, err := sanitizeMeasurementAgentName(opts.agent)
	if err != nil {
		return "", err
	}
	if opts.fullsendDir != "" {
		local, err := localMeasurementManifest(opts.fullsendDir, agent)
		if err != nil {
			return "", err
		}
		if st, err := os.Stat(local); err == nil && !st.IsDir() {
			printer.StepInfo("Using local measurement manifest " + local)
			return local, nil
		}
	}
	composeOpts, client := evalMeasureFetchContext(opts.fullsendDir, printer)
	path, ok := tryAgentsRepoMeasurementManifest(ctx, agent, client, composeOpts, printer)
	if !ok {
		return "", nil
	}
	return path, nil
}

func sanitizeMeasurementAgentName(agent string) (string, error) {
	a := strings.ToLower(strings.TrimSpace(agent))
	if a == "" || strings.ContainsAny(a, `/\`) || strings.Contains(a, "..") {
		return "", fmt.Errorf("invalid --agent name %q", agent)
	}
	return a, nil
}

func localMeasurementManifest(fullsendDir, agent string) (string, error) {
	rel := filepath.Join("eval", "measurements", agent+".yaml")
	resolved := filepath.Clean(filepath.Join(fullsendDir, rel))
	if r, err := filepath.Rel(fullsendDir, resolved); err != nil || !filepath.IsLocal(r) {
		return "", fmt.Errorf("agent name %q escapes fullsend directory", agent)
	}
	return resolved, nil
}

func evalMeasureFetchContext(fullsendDir string, printer *ui.Printer) (harness.ComposeOpts, forge.Client) {
	workspace := fullsendDir
	if workspace == "" {
		workspace = os.TempDir()
	}
	abs, err := filepath.Abs(workspace)
	if err != nil {
		abs = workspace
	}
	orgAllowlist := config.DefaultAllowedRemoteResources()
	if fullsendDir != "" && printer != nil {
		if orgCfg := tryLoadOrgConfig(filepath.Join(abs, "config.yaml"), printer); orgCfg != nil {
			orgAllowlist = orgCfg.AllowedResources()
		}
	}
	token, _ := resolveToken()
	return harness.ComposeOpts{
		WorkspaceRoot: abs,
		FetchPolicy:   fetch.DefaultPolicy,
		AuditLogPath:  filepath.Join(abs, ".fullsend-cache", "fetch-audit.jsonl"),
		OrgAllowlist:  orgAllowlist,
		GitToken:      token,
	}, gh.New(token)
}

func printMeasurementResults(printer *ui.Printer, results []evalmeasure.EvaluationResult, wroteOK bool) {
	if len(results) == 0 {
		if wroteOK {
			printer.StepDone("No new measurements (already scored or no matching traces)")
		}
		return
	}
	for _, r := range results {
		line := fmt.Sprintf("%s %s=%.2f (%s) %s", r.Version, r.Name, r.Value, r.Label, r.Explanation)
		switch r.Label {
		case evalmeasure.LabelPass:
			printer.StepDone(line)
		case evalmeasure.LabelSkip:
			printer.StepInfo(line)
		default:
			printer.StepWarn(line)
		}
	}
	if wroteOK {
		printer.StepDone(fmt.Sprintf("Wrote %d measurement(s)", len(results)))
	}
}
