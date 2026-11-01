package cli

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"

	"github.com/spf13/cobra"

	"github.com/fullsend-ai/fullsend/internal/harness"
	"github.com/fullsend-ai/fullsend/internal/ui"
)

const (
	lintOutputText = "text"
	lintOutputJSON = "json"
)

type lintJSONDiagnostic struct {
	Agent    string `json:"agent"`
	Severity string `json:"severity"`
	Field    string `json:"field"`
	Message  string `json:"message"`
}

type lintJSONSummary struct {
	Errors   int `json:"errors"`
	Warnings int `json:"warnings"`
}

type lintJSONResult struct {
	SchemaVersion int                  `json:"schema_version"`
	OK            bool                 `json:"ok"`
	Strict        bool                 `json:"strict"`
	Diagnostics   []lintJSONDiagnostic `json:"diagnostics"`
	Warnings      []string             `json:"warnings,omitempty"`
	Errors        []string             `json:"errors,omitempty"`
	Summary       lintJSONSummary      `json:"summary"`
}

// lintReporter keeps JSON output separate from the existing terminal output.
// In JSON mode, progress goes to stderr so stdout is always one JSON document.
type lintReporter struct {
	format      string
	stdout      io.Writer
	printer     *ui.Printer
	diagnostics []lintJSONDiagnostic
	warnings    []string
}

func newLintReporter(cmd *cobra.Command, format string) *lintReporter {
	progress := cmd.OutOrStdout()
	if format == lintOutputJSON {
		progress = cmd.ErrOrStderr()
	}
	return &lintReporter{
		format:  format,
		stdout:  cmd.OutOrStdout(),
		printer: ui.New(progress),
	}
}

func (r *lintReporter) warning(message string) {
	if r.format == lintOutputJSON {
		r.warnings = append(r.warnings, message)
		return
	}
	r.printer.StepWarn(message)
}

func (r *lintReporter) diagnostic(agent string, diag harness.Diagnostic) {
	if r.format == lintOutputJSON {
		r.diagnostics = append(r.diagnostics, lintJSONDiagnostic{
			Agent:    agent,
			Severity: diag.Severity.String(),
			Field:    diag.Field,
			Message:  diag.Message,
		})
		return
	}
	emitDiagnosticWithContext(r.printer, agent, diag)
}

func (r *lintReporter) finish(errs []error, errDiags, warnDiags int, strict bool) error {
	joined := errors.Join(errs...)
	if r.format != lintOutputJSON {
		return joined
	}

	result := lintJSONResult{
		SchemaVersion: 1,
		OK:            len(errs) == 0,
		Strict:        strict,
		Diagnostics:   r.diagnostics,
		Warnings:      r.warnings,
		Summary: lintJSONSummary{
			Errors:   errDiags,
			Warnings: warnDiags,
		},
	}
	if result.Diagnostics == nil {
		result.Diagnostics = []lintJSONDiagnostic{}
	}
	for _, err := range errs {
		result.Errors = append(result.Errors, err.Error())
	}
	if encodeErr := json.NewEncoder(r.stdout).Encode(result); encodeErr != nil {
		return errors.Join(joined, fmt.Errorf("writing lint JSON: %w", encodeErr))
	}
	return joined
}
