//go:build e2e

package admin

import (
	"fmt"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"

	gh "github.com/fullsend-ai/fullsend/internal/forge/github"
)

func TestIsConcurrentHeadUpdate(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want bool
	}{
		{
			name: "wrapped update-branch head race",
			err: fmt.Errorf("merge failed: %w", &gh.APIError{
				StatusCode: http.StatusUnprocessableEntity,
				Message:    "expected head sha didn’t match current head ref",
			}),
			want: true,
		},
		{
			name: "other unprocessable entity",
			err: &gh.APIError{
				StatusCode: http.StatusUnprocessableEntity,
				Message:    "pull request is not mergeable",
			},
			want: false,
		},
		{
			name: "merge conflict",
			err: &gh.APIError{
				StatusCode: http.StatusConflict,
				Message:    "head branch is out of date",
			},
			want: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, isConcurrentHeadUpdate(tt.err))
		})
	}
}
