package runtimekind

import "testing"

func TestParseAliases(t *testing.T) {
	cases := map[string]string{
		"api":              "api",
		"exec":             "subprocess",
		"claude-code":      "streaming-stdio",
		"codex-app-server": "jsonrpc-stdio",
		"opencode-serve":   "serve-http",
		"tui":              "pty",
		"debug-pty":        "pty-debug",
		"":                 "unknown",
	}
	for in, want := range cases {
		if got := Parse(in); string(got) != want {
			t.Fatalf("Parse(%q) = %q, want %q", in, got, want)
		}
	}
}
