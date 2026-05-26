// Package runtimekind normalizes provider runtime-kind vocabulary shared by
// Hollis Labs agent launchers.
package runtimekind

import (
	"strings"

	agentlaunch "github.com/hollis-labs/agentkit/agentlaunch"
)

const (
	API            agentlaunch.RuntimeKind = "api"
	Subprocess     agentlaunch.RuntimeKind = agentlaunch.RuntimeSubprocess
	StreamingStdio agentlaunch.RuntimeKind = agentlaunch.RuntimeStreamingStdio
	JSONRPCStdio   agentlaunch.RuntimeKind = agentlaunch.RuntimeJsonRpcStdio
	ServeHTTP      agentlaunch.RuntimeKind = agentlaunch.RuntimeServeHTTP
	PTY            agentlaunch.RuntimeKind = agentlaunch.RuntimePTY
	PTYDebug       agentlaunch.RuntimeKind = "pty-debug"
	Unknown        agentlaunch.RuntimeKind = "unknown"
)

// Parse normalizes catalog, CLI, and legacy alias tokens into the shared
// runtime vocabulary. It returns Unknown for empty or unrecognized values.
func Parse(raw string) agentlaunch.RuntimeKind {
	s := strings.ToLower(strings.TrimSpace(raw))
	s = strings.ReplaceAll(s, "_", "-")
	switch s {
	case "api", "provider-api", "http-api":
		return API
	case "subprocess", "exec", "cli", "single-turn", "oneshot", "one-shot":
		return Subprocess
	case "streaming-stdio", "stream-json", "streaming", "claude-code", "managed-streaming":
		return StreamingStdio
	case "jsonrpc-stdio", "json-rpc-stdio", "jsonrpc", "app-server", "codex-app-server":
		return JSONRPCStdio
	case "serve-http", "http", "sse", "opencode-serve":
		return ServeHTTP
	case "pty", "tui", "terminal":
		return PTY
	case "pty-debug", "debug-pty", "raw-pty":
		return PTYDebug
	default:
		return Unknown
	}
}

// IsKnown reports whether k is in the go-agent-runtime vocabulary. API,
// pty-debug, and unknown are intentionally handled here even though they are
// outside go-agent-launch's current RuntimeKind.Valid set.
func IsKnown(k agentlaunch.RuntimeKind) bool {
	switch k {
	case API, Subprocess, StreamingStdio, JSONRPCStdio, ServeHTTP, PTY, PTYDebug, Unknown:
		return true
	default:
		return false
	}
}

// IsManagedAutomation reports whether the runtime is suitable for unattended
// app-driven turns. Raw PTY variants are excluded because they are human/TUI
// attach paths.
func IsManagedAutomation(k agentlaunch.RuntimeKind) bool {
	switch k {
	case API, Subprocess, StreamingStdio, JSONRPCStdio, ServeHTTP:
		return true
	default:
		return false
	}
}
