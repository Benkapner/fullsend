package evalmeasure

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"
)

// ExportError wraps errors from remote score export (MLflow Assessments).
// The CLI uses errors.As to classify these as warnings (fail-open).
type ExportError struct{ Err error }

func (e *ExportError) Error() string { return e.Err.Error() }
func (e *ExportError) Unwrap() error { return e.Err }

// assessmentHTTPDoer is a seam for tests. Not safe for t.Parallel() —
// tests that swap this must not run concurrently.
var assessmentHTTPDo = func(req *http.Request) (*http.Response, error) {
	client := &http.Client{Timeout: 15 * time.Second}
	return client.Do(req)
}

type mlflowAssessmentRequest struct {
	Assessment mlflowAssessment `json:"assessment"`
}

type mlflowAssessment struct {
	AssessmentName string            `json:"assessment_name"`
	TraceID        string            `json:"trace_id"`
	Source         mlflowSource      `json:"source"`
	Feedback       mlflowFeedback    `json:"feedback"`
	Rationale      string            `json:"rationale,omitempty"`
	Metadata       map[string]string `json:"metadata,omitempty"`
	SpanID         string            `json:"span_id,omitempty"`
}

type mlflowSource struct {
	SourceType string `json:"source_type"`
	SourceID   string `json:"source_id"`
}

type mlflowFeedback struct {
	Value any `json:"value"`
}

// ExportMLflowAssessments attaches measurement scores to existing MLflow
// traces via the Assessments REST API (same surface as mlflow.log_feedback).
//
// No-op when MLFLOW_TRACKING_URI is unset and cannot be derived from the OTEL
// traces endpoint, or when tracking basic auth is unset.
//
// Auth: MLFLOW_TRACKING_USERNAME (default "admin") + MLFLOW_TRACKING_PASSWORD.
// The OTEL ingest Bearer token is not accepted by this API on the dogfood host.
func ExportMLflowAssessments(ctx context.Context, results []EvaluationResult) error {
	if len(results) == 0 {
		return nil
	}
	user, pass, hasAuth := trackingBasicAuthFromEnv()
	explicit := strings.TrimSpace(os.Getenv("MLFLOW_TRACKING_URI")) != ""
	base, err := trackingURIFromEnv()
	if err != nil {
		return err
	}
	if base == "" {
		return nil
	}
	if !hasAuth {
		if explicit {
			return &ExportError{Err: fmt.Errorf("MLFLOW_TRACKING_URI is set but MLFLOW_TRACKING_PASSWORD is empty")}
		}
		return nil
	}

	for _, r := range results {
		if err := postAssessment(ctx, base, user, pass, r); err != nil {
			return &ExportError{Err: err}
		}
	}
	return nil
}

func postAssessment(ctx context.Context, base, user, pass string, r EvaluationResult) error {
	tid := mlflowTraceID(r.TraceID)
	payload := mlflowAssessmentRequest{
		Assessment: mlflowAssessment{
			AssessmentName: r.Name,
			TraceID:        tid,
			Source: mlflowSource{
				SourceType: "CODE",
				SourceID:   "fullsend-evalmeasure/" + r.Version,
			},
			Feedback: mlflowFeedback{
				// Label is what Quality / Assessments panels display for pass/fail scorers.
				Value: r.Label,
			},
			Rationale: r.Explanation,
			Metadata: map[string]string{
				"fullsend.measurement.version":    r.Version,
				"fullsend.evaluation.score.value": fmt.Sprintf("%g", r.Value),
				"fullsend.evaluation.score.label": r.Label,
				"gen_ai.agent.name":               r.Agent,
				"fullsend.work_item_id":           r.WorkItemID,
				"fullsend.source_trace_id":        r.TraceID,
				// Full observed-vs-limit text also lives in rationale; keep a
				// copy in metadata for API consumers / UI detail panes.
				"fullsend.evaluation.explanation": r.Explanation,
			},
			SpanID: r.SpanID,
		},
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	endpoint := strings.TrimRight(base, "/") + "/ajax-api/3.0/mlflow/traces/" + url.PathEscape(tid) + "/assessments"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.SetBasicAuth(user, pass)

	resp, err := assessmentHTTPDo(req)
	if err != nil {
		return fmt.Errorf("post assessment %s: %w", r.Name, err)
	}
	defer resp.Body.Close()
	respBody, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("post assessment %s: HTTP %d: %s", r.Name, resp.StatusCode, strings.TrimSpace(string(respBody)))
	}
	return nil
}

func mlflowTraceID(hexID string) string {
	hexID = strings.TrimSpace(hexID)
	if hexID == "" {
		return hexID
	}
	if strings.HasPrefix(hexID, "tr-") {
		return hexID
	}
	return "tr-" + hexID
}

func trackingURIFromEnv() (string, error) {
	if v := strings.TrimSpace(os.Getenv("MLFLOW_TRACKING_URI")); v != "" {
		return strings.TrimRight(v, "/"), nil
	}
	// Derive from OTEL traces endpoint: https://host/v1/traces → https://host
	ep := strings.TrimSpace(os.Getenv("OTEL_EXPORTER_OTLP_TRACES_ENDPOINT"))
	if ep == "" {
		ep = strings.TrimSpace(os.Getenv("OTEL_EXPORTER_OTLP_ENDPOINT"))
	}
	if ep == "" {
		return "", nil
	}
	u, err := url.Parse(ep)
	if err != nil {
		return "", fmt.Errorf("derive MLFLOW_TRACKING_URI from OTEL endpoint %q: %w", ep, err)
	}
	if u.Scheme == "" || u.Host == "" {
		return "", fmt.Errorf("derive MLFLOW_TRACKING_URI from OTEL endpoint %q: missing scheme or host", ep)
	}
	u.Path = ""
	u.RawQuery = ""
	u.Fragment = ""
	u.User = nil
	return strings.TrimRight(u.String(), "/"), nil
}

func trackingBasicAuthFromEnv() (user, pass string, ok bool) {
	pass = strings.TrimSpace(os.Getenv("MLFLOW_TRACKING_PASSWORD"))
	if pass == "" {
		return "", "", false
	}
	user = strings.TrimSpace(os.Getenv("MLFLOW_TRACKING_USERNAME"))
	if user == "" {
		user = "admin"
	}
	return user, pass, true
}
