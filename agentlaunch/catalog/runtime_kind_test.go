package catalog

import (
	"testing"

	"github.com/hollis-labs/agent-contracts-leaf/runtimes"
)

// mapRuntimeKind is the one boundary that translates Tether's catalog
// spellings: its own two legacy tokens, and canonical runtimes.Mode
// spellings unchanged. Everything else, including the old wrapper/agentkit
// words and Tether's "api", is rejected rather than guessed.
func TestMapRuntimeKind(t *testing.T) {
	for in, want := range map[string]runtimes.Mode{
		"subprocess":          runtimes.ModeSubprocessPerTurn,
		"serve-http":          runtimes.ModeHTTPSSE,
		"subprocess-per-turn": runtimes.ModeSubprocessPerTurn,
		"streaming-stdio":     runtimes.ModeStreamingStdio,
		"jsonrpc-stdio":       runtimes.ModeJSONRPCStdio,
		"pty":                 runtimes.ModePTY,
		"http-sse":            runtimes.ModeHTTPSSE,
		"acp-stdio":           runtimes.ModeACPStdio,
		"acp-tcp":             runtimes.ModeACPTCP,
	} {
		if got, ok := mapRuntimeKind(in); !ok || got != want {
			t.Errorf("mapRuntimeKind(%q) = %q, %v; want %q", in, got, ok, want)
		}
	}
	for _, in := range []string{"", "api", "app-server", "codex-app-server", "pty-debug", "exec", "Subprocess"} {
		if got, ok := mapRuntimeKind(in); ok {
			t.Errorf("mapRuntimeKind(%q) = %q, want rejected", in, got)
		}
	}
}
