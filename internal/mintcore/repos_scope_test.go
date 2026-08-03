package mintcore

import (
	"strings"
	"testing"
)

func TestNormalizeMintRepos(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		in   []string
		want []string
	}{
		{"nil", nil, nil},
		{"empty", []string{}, []string{}},
		{"star alone", []string{"*"}, nil},
		{"star with other", []string{"*", "api"}, []string{"*", "api"}},
		{"normal", []string{"api"}, []string{"api"}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := normalizeMintRepos(tc.in)
			if len(got) != len(tc.want) {
				t.Fatalf("len=%d want %d (%v)", len(got), len(tc.want), got)
			}
			for i := range got {
				if got[i] != tc.want[i] {
					t.Fatalf("got %v want %v", got, tc.want)
				}
			}
		})
	}
}

func TestEnvTruthy(t *testing.T) {
	t.Parallel()
	for _, v := range []string{"1", "true", "TRUE", "Yes", " yes "} {
		if !EnvTruthy(v) {
			t.Fatalf("%q should be truthy", v)
		}
	}
	for _, v := range []string{"", "0", "false", "no", "on"} {
		if EnvTruthy(v) {
			t.Fatalf("%q should not be truthy", v)
		}
	}
}

func TestValidateReposScope(t *testing.T) {
	t.Parallel()
	const emptyDeny = "same-org mint requires non-empty repos"
	const selfDeny = "same-org mint requires repos to be exactly the requesting repository"
	const compatDeny = "repos scope not allowed under PER_ORG_FOREIGN_COMPAT"
	const foreignDeny = "foreign mint requires empty repos"

	tests := []struct {
		name           string
		foreign        bool
		requestingRepo string
		repos          []string
		compat         bool
		wantErrSubstr  string
		wantShape      string
	}{
		{"foreign empty", true, "fullsend-ai/fullsend", nil, false, "", ""},
		{"foreign non-empty", true, "fullsend-ai/fullsend", []string{"e2e-lock"}, false, foreignDeny, ""},
		{"same self", false, "acme/api", []string{"api"}, false, "", ""},
		{"same empty", false, "acme/api", nil, true, emptyDeny, ""},
		{"same other flag off", false, "acme/.fullsend", []string{"api"}, false, selfDeny, ""},
		{"fullsend other flag on", false, "acme/.fullsend", []string{"api"}, true, "", reposScopeShapeFullsendAny},
		{"fullsend multi flag on", false, "acme/.fullsend", []string{"a", "b", "c"}, true, "", reposScopeShapeFullsendAny},
		{"fullsend pair flag on", false, "acme/.fullsend", []string{"api", ".fullsend"}, true, "", reposScopeShapeFullsendAny},
		{"fullsend self", false, "acme/.fullsend", []string{".fullsend"}, false, "", ""},
		{"enrolled fullsend flag on", false, "acme/api", []string{".fullsend"}, true, "", reposScopeShapeEnrolledFullsend},
		{"enrolled pair flag on", false, "acme/api", []string{"api", ".fullsend"}, true, "", reposScopeShapeEnrolledPair},
		{"enrolled pair reverse", false, "acme/api", []string{".fullsend", "api"}, true, "", reposScopeShapeEnrolledPair},
		{"enrolled other flag on", false, "acme/api", []string{"other"}, true, compatDeny, ""},
		{"enrolled multi flag on", false, "acme/api", []string{"api", ".fullsend", "x"}, true, compatDeny, ""},
		{"enrolled pair flag off", false, "acme/api", []string{"api", ".fullsend"}, false, selfDeny, ""},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			shape, err := validateReposScope(tc.foreign, tc.requestingRepo, tc.repos, tc.compat)
			if tc.wantErrSubstr == "" {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				if shape != tc.wantShape {
					t.Fatalf("shape=%q want %q", shape, tc.wantShape)
				}
				return
			}
			if err == nil {
				t.Fatal("expected error")
			}
			if shape != "" {
				t.Fatalf("expected empty shape on error, got %q", shape)
			}
			if !strings.Contains(err.Error(), tc.wantErrSubstr) {
				t.Fatalf("error %q does not contain %q", err.Error(), tc.wantErrSubstr)
			}
		})
	}
}
