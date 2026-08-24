package cli

import (
	"bytes"
	"context"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/fullsend-ai/fullsend/internal/forge"
	"github.com/fullsend-ai/fullsend/internal/ui"
)

// appendStaleVendoredDeletes must turn manifest-recorded paths that left the
// vendored set into Delete tree entries, and pass through cleanly when no
// manifest exists (first vendor install) or the manifest is unreadable.
func TestAppendStaleVendoredDeletes(t *testing.T) {
	ctx := context.Background()
	printer := ui.New(&bytes.Buffer{})

	newFiles := []forge.TreeFile{
		{Path: ".defaults/action.yml", Content: []byte("a"), Mode: "100644"},
		{Path: ".defaults/.github/scripts/check-fix-eligibility.sh", Content: []byte("b"), Mode: "100755"},
	}

	t.Run("prunes de-listed manifest paths", func(t *testing.T) {
		client := forge.NewFakeClient()
		client.FileContents["o/r/.fullsend/vendor-manifest.yaml"] = []byte(
			"version: \"1\"\n" +
				"binary_path: .defaults/bin/fullsend\n" +
				"cli_version: v0.36.0\n" +
				"paths:\n" +
				"  - .defaults/action.yml\n" +
				"  - .defaults/.github/scripts/check-fix-eligibility.sh\n" +
				"  - .defaults/.github/scripts/redact-behaviour-artifacts.sh\n" +
				"  - .defaults/.github/scripts/redact-behaviour-artifacts-test.sh\n")

		out := appendStaleVendoredDeletes(ctx, client, printer, "o", "r", newFiles)

		var deletes []string
		for _, f := range out {
			if f.Delete {
				deletes = append(deletes, f.Path)
			}
		}
		assert.Equal(t, []string{
			".defaults/.github/scripts/redact-behaviour-artifacts-test.sh",
			".defaults/.github/scripts/redact-behaviour-artifacts.sh",
		}, deletes)
		assert.Len(t, out, len(newFiles)+2)
	})

	t.Run("no manifest is a clean pass-through", func(t *testing.T) {
		client := forge.NewFakeClient()
		out := appendStaleVendoredDeletes(ctx, client, printer, "o", "r", newFiles)
		assert.Equal(t, newFiles, out)
	})

	t.Run("unreadable manifest skips pruning instead of failing the vendor", func(t *testing.T) {
		client := forge.NewFakeClient()
		client.FileContents["o/r/.fullsend/vendor-manifest.yaml"] = []byte("{not yaml")
		out := appendStaleVendoredDeletes(ctx, client, printer, "o", "r", newFiles)
		assert.Equal(t, newFiles, out)
	})
}
