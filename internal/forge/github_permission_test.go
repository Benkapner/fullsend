package forge

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func permissionFlags(admin, maintain, push, triage, pull bool) *GitHubPermissionFlags {
	return &GitHubPermissionFlags{
		Admin:    &admin,
		Maintain: &maintain,
		Push:     &push,
		Triage:   &triage,
		Pull:     &pull,
	}
}

func TestResolveGitHubCollaboratorPermission(t *testing.T) {
	tests := []struct {
		name    string
		input   GitHubCollaboratorPermission
		want    string
		wantErr string
	}{
		{
			name:  "built-in role remains supported without extra signals",
			input: GitHubCollaboratorPermission{RoleName: "write"},
			want:  "write",
		},
		{
			name: "reported custom maintain role",
			input: GitHubCollaboratorPermission{
				RoleName:   "ODH Repo Maintainer",
				Permission: "write",
				User: GitHubPermissionUser{
					Permissions: permissionFlags(false, true, true, true, true),
				},
			},
			want: "maintain",
		},
		{
			name: "custom triage role",
			input: GitHubCollaboratorPermission{
				RoleName:   "Support Triage",
				Permission: "read",
				User: GitHubPermissionUser{
					Permissions: permissionFlags(false, false, false, true, true),
				},
			},
			want: "triage",
		},
		{
			name: "custom admin role",
			input: GitHubCollaboratorPermission{
				RoleName:   "Security Administrator",
				Permission: "admin",
				User: GitHubPermissionUser{
					Permissions: permissionFlags(true, true, true, true, true),
				},
			},
			want: "admin",
		},
		{
			name: "custom read role",
			input: GitHubCollaboratorPermission{
				RoleName:   "Documentation Reader",
				Permission: "read",
				User: GitHubPermissionUser{
					Permissions: permissionFlags(false, false, false, false, true),
				},
			},
			want: "read",
		},
		{
			name: "custom role without access",
			input: GitHubCollaboratorPermission{
				RoleName:   "No Access",
				Permission: "none",
				User: GitHubPermissionUser{
					Permissions: permissionFlags(false, false, false, false, false),
				},
			},
			want: "none",
		},
		{
			name: "built-in maintain accepts collapsed legacy and precise signals",
			input: GitHubCollaboratorPermission{
				RoleName:   "maintain",
				Permission: "write",
				User: GitHubPermissionUser{
					Permissions: permissionFlags(false, true, true, true, true),
				},
			},
			want: "maintain",
		},
		{
			name:  "custom role falls back to conservative legacy write",
			input: GitHubCollaboratorPermission{RoleName: "Custom Write", Permission: "write"},
			want:  "write",
		},
		{
			name:  "custom role falls back to conservative legacy read",
			input: GitHubCollaboratorPermission{RoleName: "Custom Triage", Permission: "read"},
			want:  "read",
		},
		{
			name:    "custom role without effective permission fails closed",
			input:   GitHubCollaboratorPermission{RoleName: "Custom"},
			wantErr: "no effective permission",
		},
		{
			name: "incomplete flags fail closed",
			input: GitHubCollaboratorPermission{
				RoleName:   "Custom",
				Permission: "write",
				User: GitHubPermissionUser{
					Permissions: &GitHubPermissionFlags{},
				},
			},
			wantErr: "missing required boolean fields",
		},
		{
			name: "explicit null flags fail closed",
			input: GitHubCollaboratorPermission{
				RoleName:   "Custom",
				Permission: "write",
				User: GitHubPermissionUser{
					permissionsPresent: true,
				},
			},
			wantErr: "missing required boolean fields",
		},
		{
			name: "contradictory hierarchy fails closed",
			input: GitHubCollaboratorPermission{
				RoleName:   "Custom",
				Permission: "write",
				User: GitHubPermissionUser{
					Permissions: permissionFlags(false, true, false, true, true),
				},
			},
			wantErr: "contradictory capability flags",
		},
		{
			name: "legacy and precise signals conflict",
			input: GitHubCollaboratorPermission{
				RoleName:   "Custom",
				Permission: "read",
				User: GitHubPermissionUser{
					Permissions: permissionFlags(false, false, true, true, true),
				},
			},
			wantErr: "conflicts with permission",
		},
		{
			name:    "built-in and legacy signals conflict",
			input:   GitHubCollaboratorPermission{RoleName: "admin", Permission: "write"},
			wantErr: "conflicts with permission",
		},
		{
			name:    "unknown legacy permission fails closed",
			input:   GitHubCollaboratorPermission{RoleName: "Custom", Permission: "maintain"},
			wantErr: "unknown legacy permission",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ResolveGitHubCollaboratorPermission(tt.input)
			if tt.wantErr != "" {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.wantErr)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tt.want, got)
		})
	}
}
