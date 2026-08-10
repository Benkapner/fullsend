package evalmeasure

import (
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestExportMLflowAssessments_NoopWithoutURI(t *testing.T) {
	t.Setenv("MLFLOW_TRACKING_URI", "")
	t.Setenv("OTEL_EXPORTER_OTLP_TRACES_ENDPOINT", "")
	t.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", "")
	t.Setenv("MLFLOW_TRACKING_PASSWORD", "")
	err := ExportMLflowAssessments(t.Context(), []EvaluationResult{{
		Name: "trace_fitness", TraceID: "aa", Label: "pass", Value: 1,
	}})
	assert.NoError(t, err)
}

func TestExportMLflowAssessments_PostsPassLabel(t *testing.T) {
	t.Setenv("MLFLOW_TRACKING_URI", "https://mlflow.example")
	t.Setenv("MLFLOW_TRACKING_USERNAME", "admin")
	t.Setenv("MLFLOW_TRACKING_PASSWORD", "secret")

	var gotURL string
	var gotBody string
	var gotAuth string
	orig := assessmentHTTPDo
	t.Cleanup(func() { assessmentHTTPDo = orig })
	assessmentHTTPDo = func(req *http.Request) (*http.Response, error) {
		gotURL = req.URL.String()
		gotAuth = req.Header.Get("Authorization")
		b, _ := io.ReadAll(req.Body)
		gotBody = string(b)
		return &http.Response{
			StatusCode: 200,
			Body:       io.NopCloser(strings.NewReader(`{"assessment":{"assessment_id":"a-1"}}`)),
			Header:     make(http.Header),
		}, nil
	}

	err := ExportMLflowAssessments(t.Context(), []EvaluationResult{{
		Name:        "trace_fitness",
		TraceID:     "76f79b7475310c3a16d306b98076dd6c",
		SpanID:      "c29fc9fc1877260b",
		Label:       "pass",
		Value:       1.0,
		Explanation: "all checks passed",
		Version:     "em-001@1",
		Agent:       "triage",
		WorkItemID:  "https://github.com/fullsend-ai/fullsend/issues/1342",
	}})
	require.NoError(t, err)
	assert.Equal(t, "https://mlflow.example/ajax-api/3.0/mlflow/traces/tr-76f79b7475310c3a16d306b98076dd6c/assessments", gotURL)
	assert.True(t, strings.HasPrefix(gotAuth, "Basic "))
	assert.Contains(t, gotBody, `"assessment_name":"trace_fitness"`)
	assert.Contains(t, gotBody, `"value":"pass"`)
	assert.Contains(t, gotBody, `"source_type":"CODE"`)
	assert.Contains(t, gotBody, `"fullsend-evalmeasure/em-001@1"`)
}

func TestExportMLflowAssessments_DerivedURIWithoutPasswordIsNoop(t *testing.T) {
	t.Setenv("MLFLOW_TRACKING_URI", "")
	t.Setenv("OTEL_EXPORTER_OTLP_TRACES_ENDPOINT", "https://mlflow.example/v1/traces")
	t.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", "")
	t.Setenv("MLFLOW_TRACKING_PASSWORD", "")
	err := ExportMLflowAssessments(t.Context(), []EvaluationResult{{
		Name: "trace_fitness", TraceID: "aa", Label: "pass", Value: 1,
	}})
	assert.NoError(t, err, "derived URI without password should be a silent no-op")
}

func TestExportMLflowAssessments_ExplicitURIWithoutPasswordErrors(t *testing.T) {
	t.Setenv("MLFLOW_TRACKING_URI", "https://mlflow.example")
	t.Setenv("OTEL_EXPORTER_OTLP_TRACES_ENDPOINT", "")
	t.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", "")
	t.Setenv("MLFLOW_TRACKING_PASSWORD", "")
	err := ExportMLflowAssessments(t.Context(), []EvaluationResult{{
		Name: "trace_fitness", TraceID: "aa", Label: "pass", Value: 1,
	}})
	require.Error(t, err)
	var exportErr *ExportError
	assert.ErrorAs(t, err, &exportErr)
	assert.Contains(t, err.Error(), "MLFLOW_TRACKING_PASSWORD is empty")
}

func TestTrackingURIFromEnv_DerivesFromOTLP(t *testing.T) {
	t.Setenv("MLFLOW_TRACKING_URI", "")
	t.Setenv("OTEL_EXPORTER_OTLP_TRACES_ENDPOINT", "https://mlflow.example.test/v1/traces")
	uri, err := trackingURIFromEnv()
	require.NoError(t, err)
	assert.Equal(t, "https://mlflow.example.test", uri)
}

func TestMlflowTraceID(t *testing.T) {
	assert.Equal(t, "tr-abc", mlflowTraceID("abc"))
	assert.Equal(t, "tr-abc", mlflowTraceID("tr-abc"))
}
