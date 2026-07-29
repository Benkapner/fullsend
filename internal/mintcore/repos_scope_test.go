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
		if !envTruthy(v) {
			t.Fatalf("%q should be truthy", v)
		}
	}
	for _, v := range []string{"", "0", "false", "no", "on"} {
		if envTruthy(v) {
			t.Fatalf("%q should not be truthy", v)
		}
	}
}

func TestValidateReposScope(t *testing.T) {
	t.Parallel()
	const deny = "repos scope not allowed"
	const foreignDeny = "foreign mint requires empty repos"

	tests := []struct {
		name           string
		foreign        bool
		requestingRepo string
		repos          []string
		compat         bool
		wantErrSubstr  string
	}{
		{"foreign empty", true, "fullsend-ai/fullsend", nil, false, ""},
		{"foreign non-empty", true, "fullsend-ai/fullsend", []string{"e2e-lock"}, false, foreignDeny},
		{"same self", false, "acme/api", []string{"api"}, false, ""},
		{"same empty", false, "acme/api", nil, true, deny},
		{"same other flag off", false, "acme/.fullsend", []string{"api"}, false, deny},
		{"fullsend other flag on", false, "acme/.fullsend", []string{"api"}, true, ""},
		{"fullsend multi flag on", false, "acme/.fullsend", []string{"a", "b", "c"}, true, ""},
		{"fullsend pair flag on", false, "acme/.fullsend", []string{"api", ".fullsend"}, true, ""},
		{"fullsend self", false, "acme/.fullsend", []string{".fullsend"}, false, ""},
		{"enrolled fullsend flag on", false, "acme/api", []string{".fullsend"}, true, ""},
		{"enrolled pair flag on", false, "acme/api", []string{"api", ".fullsend"}, true, ""},
		{"enrolled pair reverse", false, "acme/api", []string{".fullsend", "api"}, true, ""},
		{"enrolled other flag on", false, "acme/api", []string{"other"}, true, deny},
		{"enrolled multi flag on", false, "acme/api", []string{"api", ".fullsend", "x"}, true, deny},
		{"enrolled pair flag off", false, "acme/api", []string{"api", ".fullsend"}, false, deny},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			err := validateReposScope(tc.foreign, tc.requestingRepo, tc.repos, tc.compat)
			if tc.wantErrSubstr == "" {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				return
			}
			if err == nil {
				t.Fatal("expected error")
			}
			if !strings.Contains(err.Error(), tc.wantErrSubstr) {
				t.Fatalf("error %q does not contain %q", err.Error(), tc.wantErrSubstr)
			}
		})
	}
}
