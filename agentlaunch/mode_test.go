package agentlaunch

import "testing"

func TestLaunchModeValid(t *testing.T) {
	cases := []struct {
		name string
		in   LaunchMode
		want bool
	}{
		{"interactive", LaunchInteractive, true},
		{"background", LaunchBackground, true},
		{"ephemeral", LaunchEphemeral, true},
		{"empty zero value", LaunchMode(""), false},
		{"unknown", LaunchMode("detached"), false},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.in.Valid(); got != tc.want {
				t.Fatalf("LaunchMode(%q).Valid() = %v, want %v", tc.in, got, tc.want)
			}
		})
	}
}

func TestLaunchModeString(t *testing.T) {
	cases := []struct {
		in   LaunchMode
		want string
	}{
		{LaunchInteractive, "interactive"},
		{LaunchBackground, "background"},
		{LaunchEphemeral, "ephemeral"},
		{LaunchMode(""), ""},
	}
	for _, tc := range cases {
		if got := tc.in.String(); got != tc.want {
			t.Errorf("LaunchMode(%q).String() = %q, want %q", string(tc.in), got, tc.want)
		}
	}
}
