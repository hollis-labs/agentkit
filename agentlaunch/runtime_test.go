package agentlaunch

import "testing"

func TestRuntimeKindValid(t *testing.T) {
	cases := []struct {
		name string
		in   RuntimeKind
		want bool
	}{
		{"subprocess", RuntimeSubprocess, true},
		{"pty", RuntimePTY, true},
		{"streaming-stdio", RuntimeStreamingStdio, true},
		{"jsonrpc-stdio", RuntimeJsonRpcStdio, true},
		{"empty zero value", RuntimeKind(""), false},
		{"unknown", RuntimeKind("websocket"), false},
		{"trailing space", RuntimeKind("pty "), false},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.in.Valid(); got != tc.want {
				t.Fatalf("RuntimeKind(%q).Valid() = %v, want %v", tc.in, got, tc.want)
			}
		})
	}
}

func TestRuntimeKindString(t *testing.T) {
	cases := []struct {
		in   RuntimeKind
		want string
	}{
		{RuntimeSubprocess, "subprocess"},
		{RuntimePTY, "pty"},
		{RuntimeStreamingStdio, "streaming-stdio"},
		{RuntimeJsonRpcStdio, "jsonrpc-stdio"},
		{RuntimeKind(""), ""},
	}
	for _, tc := range cases {
		if got := tc.in.String(); got != tc.want {
			t.Errorf("RuntimeKind(%q).String() = %q, want %q", string(tc.in), got, tc.want)
		}
	}
}
