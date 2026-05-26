package agentlaunch

import "testing"

func TestWorkspaceModeValid(t *testing.T) {
	cases := []struct {
		name string
		in   WorkspaceMode
		want bool
	}{
		{"shared", WorkspaceShared, true},
		{"temp", WorkspaceTemp, true},
		{"fresh", WorkspaceFresh, true},
		{"persistent", WorkspacePersistent, true},
		{"empty zero value", WorkspaceMode(""), false},
		{"unknown", WorkspaceMode("scratch"), false},
		{"mixed case", WorkspaceMode("Shared"), false},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.in.Valid(); got != tc.want {
				t.Fatalf("WorkspaceMode(%q).Valid() = %v, want %v", tc.in, got, tc.want)
			}
		})
	}
}

func TestWorkspaceModeString(t *testing.T) {
	cases := []struct {
		in   WorkspaceMode
		want string
	}{
		{WorkspaceShared, "shared"},
		{WorkspaceTemp, "temp"},
		{WorkspaceFresh, "fresh"},
		{WorkspacePersistent, "persistent"},
		{WorkspaceMode(""), ""},
	}
	for _, tc := range cases {
		if got := tc.in.String(); got != tc.want {
			t.Errorf("WorkspaceMode(%q).String() = %q, want %q", string(tc.in), got, tc.want)
		}
	}
}
